//go:build unix

package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/terminalvt"
)

// FuzzTerminalLeaseStateMachine covers service leases, not session/network resume.
// One fixed acquisition plus at most fifteen decoded actions bounds each case.
func FuzzTerminalLeaseStateMachine(f *testing.F) {
	f.Add([]byte{4, 7, 6, 7, 9, 8, 4, 1, 0, 14, 20})
	f.Add([]byte{16, 17, 0, 1, 16, 20, 2, 0, 4, 18, 16, 20})
	f.Add([]byte{10, 11, 3, 0, 2, 0, 5, 12, 7, 13, 15})
	f.Add([]byte{8, 4}) // A rejected first gap must not claim a stream identity.
	f.Add([]byte{13, 4})
	f.Add([]byte{6, 8, 7, 13, 7, 4}) // Rejections retain the typed failure receipt.
	f.Add([]byte{42, 4, 6, 7, 12, 7})
	f.Fuzz(func(t *testing.T, actions []byte) {
		if len(actions) > 15 {
			actions = actions[:15]
		}
		s, _ := testTerminalService(t)
		capture := filepath.Join(t.TempDir(), "input")
		info, err := s.Create(TerminalCreateOpts{Command: "/bin/sh", Args: []string{
			"-c", `stty raw -echo; printf READY; exec cat > "$1"`, "lease-fuzz", capture,
		}, Cols: 40, Rows: 4})
		if errors.Is(err, terminalvt.ErrNoCGO) {
			t.Skip(err)
		}
		if err != nil {
			t.Fatal(err)
		}
		dirty, cancel, err := s.Subscribe(info.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer cancel()
		ready := time.NewTimer(3 * time.Second)
		defer ready.Stop()
		for {
			p, err := s.Rows(info.ID, terminalvt.RowRequest{Start: -1})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(terminalText(p), "READY") {
				break
			}
			select {
			case <-dirty:
			case <-ready.C:
				t.Fatal("raw input child did not start")
			}
		}
		owners := [2]TerminalController{{"a", 1}, {"b", 1}}
		known := map[TerminalController]bool{owners[0]: true, owners[1]: true}
		retired := make(map[TerminalController]bool)
		active := TerminalLease{info.ID, owners[0], 1}
		latest, stale := [2]TerminalLease{active, {}}, [2]TerminalLease{}
		generation, sequence := uint64(1), uint64(0)
		stream := ""
		receipts := make(map[uint64]error)
		var wantBytes []byte
		if got, err := s.Acquire(info.ID, owners[0]); err != nil || got != active {
			t.Fatalf("initial acquire: %v %v", got, err)
		}
		check := func(step int, got, want error) {
			t.Helper()
			if (want == nil && got != nil) || (want != nil && !errors.Is(got, want)) {
				t.Fatalf("actions=%x step=%d: error=%v want=%v", actions, step, got, want)
			}
		}
		observe := func() {
			t.Helper()
			p, err := s.Rows(info.ID, terminalvt.RowRequest{Start: -1})
			if err != nil || p.Grid.Cols != 40 || p.Grid.GridRows != 4 || !p.Info.Alive {
				t.Fatalf("rejected intents changed grid/process: %+v err=%v", p.Info, err)
			}
			count := 0
			for owner := range known {
				got, ok := s.ControllerLease(info.ID, owner)
				want := active.Generation != 0 && active.Owner == owner
				if ok != want || (ok && got != active) || (!ok && got != (TerminalLease{})) {
					t.Fatalf("controller %v: got=%v/%v want=%v active=%v", owner, got, ok, want, active)
				}
				if ok {
					count++
				}
			}
			if count > 1 || (active.Generation != 0 && count != 1) {
				t.Fatalf("controller count=%d active=%v", count, active)
			}
		}
		observe()
		for step, action := range actions {
			op, actor := action&15, int(action>>4)&1
			switch op {
			case 0: // Repeated acquire is idempotent; reacquire is a new generation.
				owner := owners[actor]
				var want error
				if retired[owner] || (active.Generation != 0 && active.Owner != owner) {
					want = ErrTerminalControl
				}
				got, err := s.Acquire(info.ID, owner)
				check(step, err, want)
				if want == nil {
					if active.Generation == 0 {
						generation++
						active = TerminalLease{info.ID, owner, generation}
						stream, sequence, receipts = "", 0, make(map[uint64]error)
					}
					if got != active {
						t.Fatalf("acquire=%v want=%v", got, active)
					}
					if latest[actor] != got {
						stale[actor], latest[actor] = latest[actor], got
					}
				} else if got != (TerminalLease{}) {
					t.Fatal("rejected acquire returned authority", got)
				}
			case 1:
				want := ErrTerminalControl
				if latest[actor].Generation != 0 && latest[actor] == active {
					want, active = nil, TerminalLease{}
				}
				check(step, s.Release(latest[actor]), want)
			case 2, 3:
				owner := owners[actor]
				if op == 3 && latest[actor].Generation != 0 {
					owner = latest[actor].Owner // Delayed detach must not revoke a new epoch.
				}
				s.Detach(owner)
				retired[owner], known[owner] = true, true
				if active.Owner == owner {
					active = TerminalLease{}
				}
				if op == 2 {
					owners[actor].Epoch++
					known[owners[actor]] = true
				}
			case 15:
				_, err := s.Acquire(info.ID, TerminalController{ClientID: "invalid"})
				check(step, err, ErrTerminalControl)
			default:
				lease := latest[actor]
				switch op {
				case 10:
					lease.Generation += 100
					if action&32 != 0 {
						lease.TerminalID += "-wrong"
					}
				case 11:
					lease.Owner.Epoch += 100
				case 14:
					lease = stale[actor]
				}
				payload := []byte{byte('A' + step)}
				input := TerminalInput{Lease: lease, Kind: "paste", Data: payload}
				switch op {
				case 5:
					input.Kind, input.Key = "key", terminalvt.Key{Code: rune(payload[0]), Text: string(payload)}
				case 6:
					input.Kind, input.Cols, input.Rows = "resize", terminalvt.MaxCols+1, 4
				}
				name, seq, ack := "input", sequence+1, uint64(0)
				switch op {
				case 7:
					seq = sequence // Retransmit with changed payload: never execute again.
				case 8:
					seq, name = sequence+2, "gap"
					if stream != "" {
						name, ack = stream, sequence
					}
				case 9:
					name = "changed"
				case 12:
					ack = sequence
				case 13:
					ack, name = sequence+1, "future-ack"
					if stream != "" {
						name = stream
					}
				}
				// Independent log oracle: rejected control/order never commits a stream,
				// sequence, receipt or bytes. Only accepted contiguous intents do so.
				var want error
				switch {
				case lease.Generation == 0 || lease != active:
					want = ErrTerminalControl
				case (stream != "" && stream != name) || ack > sequence || seq == 0 || seq > sequence+1:
					want = ErrTerminalInputSequence
				default:
					for n := range receipts {
						if n <= ack {
							delete(receipts, n)
						}
					}
					if seq <= sequence {
						want = receipts[seq]
					} else {
						stream, sequence = name, seq
						if op == 6 {
							want = terminalvt.ErrBounds
							receipts[seq] = want
						} else {
							wantBytes = append(wantBytes, payload...)
						}
					}
				}
				check(step, s.ApplyInput(name, seq, ack, input), want)
			}
			observe()
		}
		// This FIFO barrier is fixture plumbing, not controller authority. The raw
		// child records every actual accepted byte, including unexpected duplicates.
		const barrier = "~DONE~"
		if err := s.manager.Write(info.ID, []byte(barrier)); err != nil {
			t.Fatal(err)
		}
		wantBytes = append(wantBytes, barrier...)
		deadline := time.Now().Add(3 * time.Second)
		for {
			got, err := os.ReadFile(capture)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if bytes.Contains(got, []byte(barrier)) {
				if !bytes.Equal(got, wantBytes) {
					t.Fatalf("actions=%x raw child bytes=%q want=%q", actions, got, wantBytes)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("raw child barrier missing: %q want=%q", got, wantBytes)
			}
			time.Sleep(time.Millisecond)
		}
	})
}
