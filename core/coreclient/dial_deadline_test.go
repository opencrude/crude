package coreclient

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"crude/core/coreprotocol"
	"crude/core/corewire"
)

func TestDialHonorsCallerDeadlineAcrossSlowCarrierStartup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		budget  time.Duration
		timeout bool
	}{
		{"caller allows slow startup", 30 * time.Second, false},
		{"caller cancels startup", 5 * time.Second, true},
		{"no deadline keeps default bound", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const startup = 12 * time.Second
				ctx := t.Context()
				if tc.budget > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.budget)
					defer cancel()
				}
				f := &fakeCore{t: t, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) {
					reply(s, &coreprotocol.BoolResult{OK: true})
				}}
				client, err := Dial(ctx, func(ctx context.Context) (corewire.Conn, error) {
					a, b := net.Pipe()
					go func() {
						// OpenSSH returns its pipes before the remote
						// process starts reading them.
						select {
						case <-time.After(startup):
							f.serve(b)
						case <-ctx.Done():
							_ = b.Close()
						}
					}()
					return corewire.NewMux(a, corewire.CarrierLocal, true), nil
				}, "slow-startup")
				if tc.timeout {
					if client != nil {
						_ = client.Close()
						t.Fatal("session attached after the deadline")
					}
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("slow connection did not report the deadline: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				var result coreprotocol.BoolResult
				if err := client.Call(ctx, "workspace", "ready", &coreprotocol.Empty{}, &result); err != nil || !result.OK {
					t.Fatalf("session not usable after slow startup: %+v %v", result, err)
				}
			})
		})
	}
}

func TestResumeHasTimeToReestablishTheOriginalCarrier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		f := &fakeCore{t: t, grace: time.Minute, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) { reply(s, &coreprotocol.BoolResult{OK: true}) }}
		first := true
		c, err := Dial(ctx, func(ctx context.Context) (corewire.Conn, error) {
			if !first {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(12 * time.Second):
				}
			}
			first = false
			return f.dialer()(ctx)
		}, "slow-resume")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()

		f.dropCarriers()
		synctest.Wait()
		var got coreprotocol.BoolResult
		if err := c.Call(ctx, "workspace", "after-cut", &coreprotocol.Empty{}, &got); err != nil || !got.OK {
			t.Fatalf("could not use the retained session after a slow redial: %v", err)
		}
	})
}
