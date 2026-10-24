package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
)

func TestStateTopicResetWaitsForCapturedSourceGeneration(t *testing.T) {
	for _, unchanged := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed", true: "deduplicated"}[unchanged], func(t *testing.T) {
			r, root := resourceRuntime(t)
			client, ws := topicResourceConnection(t, r, root, "source-fence")
			updates := make(chan coreprotocol.WorkspaceState, 4)
			subscribe := func() interface{ Close() } {
				return client.Subscribe(t.Context(), ws, coreprotocol.TopicSettings, "", func(_ coreprotocol.TopicHeader, body []byte) error {
					var value coreprotocol.WorkspaceState
					if err := coreprotocol.Decode(body, &value); err != nil {
						return err
					}
					select {
					case updates <- value:
					case <-t.Context().Done():
						return t.Context().Err()
					}
					return nil
				})
			}
			first := subscribe()
			var initial coreprotocol.WorkspaceState
			select {
			case initial = <-updates:
			case <-time.After(2 * time.Second):
				t.Fatal("initial settings not published")
			}
			first.Close()
			if initial.Settings == nil {
				t.Fatal("missing settings projection")
			}
			r.mu.Lock()
			a := r.workspaces[ws]
			r.mu.Unlock()
			slot := a.states.slots[coreprotocol.TopicSettings]
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			candidate := initial
			settings := *initial.Settings
			candidate.Settings = &settings
			if !unchanged {
				candidate.Settings.Application.Theme.Preset = "light"
			}
			if err := a.inspect(t.Context(), func() {
				slot.mu.Lock()
				slot.wanted++
				slot.pending = func(ctx context.Context) coreprotocol.WorkspaceState {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
					}
					return candidate
				}
				slot.mu.Unlock()
				select {
				case slot.wake <- struct{}{}:
				default:
				}
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("derivation did not start")
			}
			next := subscribe()
			t.Cleanup(next.Close)
			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			for {
				slot.mu.Lock()
				waiting := slot.changed != nil
				slot.mu.Unlock()
				if waiting {
					break
				}
				select {
				case <-updates:
					t.Fatal("reset published the pre-command snapshot before its source finished")
				case <-deadline.C:
					t.Fatal("reset did not reach its source-generation wait")
				case <-time.After(time.Millisecond):
				}
			}
			select {
			case <-updates:
				t.Fatal("source boundary was acknowledged early")
			default:
			}
			unblock()
			select {
			case value := <-updates:
				if value.Settings == nil || value.Settings.Application.Theme.Preset != candidate.Settings.Application.Theme.Preset {
					t.Fatal("reset did not publish the completed source")
				}
			case <-time.After(time.Second):
				t.Fatal("source completion did not release reset, including byte deduplication")
			}
		})
	}
}
