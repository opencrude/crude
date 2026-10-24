package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/metrics"
	"crude/workspace"
)

// One fixed registration per worker, not one new registry entry per fuzz case.
// Its authored increments have no dependency on nondeterministic runtime data.
var semanticMetricsCounter = metrics.NewCounter(metrics.Meta{Name: "fuzz.metrics.authored", Group: metrics.GroupCore, Title: "Authored command fixture", Unit: "items"})

// Native fuzz cases execute serially within each worker process. Keep the
// authored lifetime total independently; it must not be read back from Take.
var semanticMetricsExpected uint64

func semanticMetricTotal(snapshot metrics.Snapshot) uint64 {
	for _, series := range snapshot.Counters {
		if series.Meta.Name == "fuzz.metrics.authored" {
			return series.Total
		}
	}
	return 0
}

func semanticMetricsShape(t *testing.T, s metrics.Snapshot, q metrics.Query, total uint64) {
	t.Helper()
	// Independent contract oracle: enumerate the selected window's buckets,
	// without calling Query.normalized or using a production snapshot as expected.
	seconds := q.Seconds
	if seconds <= 0 {
		seconds = 120
	} else if seconds > 300 {
		seconds = 300
	}
	budget := q.Points
	if budget <= 0 || budget > seconds {
		budget = seconds
	}
	step := 1
	for step*budget < seconds {
		step++
	}
	points := 0
	for covered := 0; covered < seconds; covered += step {
		points++
	}
	if s.Step != step || s.PID != os.Getpid() || s.Process == "" || s.Taken.IsZero() || s.End != s.Taken.Unix() {
		t.Fatal("metrics identity/window metadata invalid")
	}
	for _, series := range s.Latencies {
		if len(series.Points) != points {
			t.Fatal("latency window exceeded query")
		}
	}
	for _, series := range s.Counters {
		if len(series.Points) != points {
			t.Fatal("counter window exceeded query")
		}
		var sum uint64
		for _, n := range series.Points {
			sum += n
		}
		if sum != series.Window {
			t.Fatal("counter window disagrees with points")
		}
	}
	for _, series := range s.Gauges {
		if len(series.Points) != points {
			t.Fatal("gauge window exceeded query")
		}
	}
	for _, series := range s.Tables {
		if len(series.Rows) > 24 {
			t.Fatal("table row bound exceeded")
		}
		for i, row := range series.Rows {
			if i < 8 && len(row.Points) != points || i >= 8 && len(row.Points) != 0 {
				t.Fatal("table point bound exceeded")
			}
		}
	}
	if len(s.Events) > 256 || semanticMetricTotal(s) != total {
		t.Fatalf("fixture/event bounds: total %d want %d", semanticMetricTotal(s), total)
	}
}

func semanticRawMetrics(t *testing.T, w *semanticFuzzWire, id string, input coreprotocol.Value) coreprotocol.Reply {
	t.Helper()
	s, err := w.currentCarrier().OpenStream(w.ctx, corewire.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	semanticSend(t, s, &coreprotocol.Open{Kind: coreprotocol.StreamCommand, ID: id, Method: coreprotocol.CoreMetricsMethod}, input)
	body, err := corewire.NewReader(s, corewire.MaxMessage).Next()
	if err != nil {
		t.Fatal(err)
	}
	var reply coreprotocol.Reply
	if _, err = coreprotocol.DecodePrefix(body, &reply); err != nil || !reply.Final {
		t.Fatal("invalid command reply", err)
	}
	semanticSend(t, s, &coreprotocol.TopicAck{Seq: 1})
	_ = s.CloseWrite()
	_, _ = corewire.NewReader(s, corewire.MaxMessage).Next()
	return reply
}

// A maximum of eight actions per case. Query integers are unrestricted (negative,
// zero and machine limits included); returned histories remain <=300 points.
// Detached startup clients need no workspace. A forged Owner is additionally
// checked at Dispatch, because wire commands inherit, never supply, that identity.
func FuzzCoreMetricsRequests(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7}, 0, 0)
	f.Add([]byte{4, 5, 0, 7, 6, 3, 2, 1}, 301, -1)
	f.Add([]byte{0, 0, 4, 7}, 299, 7)
	f.Add([]byte{0, 6}, -int(^uint(0)>>1)-1, int(^uint(0)>>1))
	// Minimized race reproduction: an invalid command identity must return its
	// typed refusal rather than resetting the stream before the peer sees it.
	f.Add([]byte{3}, 0, 0)
	f.Fuzz(func(t *testing.T, actions []byte, seconds, points int) {
		t.Setenv("CRUDE_HOME", t.TempDir())
		r, err := New(func(string) (*workspace.Workspace, error) {
			t.Error("metrics constructed workspace")
			return nil, fmt.Errorf("metrics must be detached")
		}, filepath.Join(t.TempDir(), "receipts.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := r.Close(); err != nil {
				t.Error(err)
			}
		})
		w := newSemanticFuzzWire(t, r, "metrics-semantic")
		q := metrics.Query{Seconds: seconds, Points: points}
		take := func() metrics.Snapshot {
			var s metrics.Snapshot
			if err := w.c.Call(w.ctx, "", coreprotocol.CoreMetricsMethod, &q, &s); err != nil {
				t.Fatal(err)
			}
			return s
		}
		initial := take()
		total := semanticMetricsExpected
		semanticMetricsShape(t, initial, q, total)
		held, err := coreprotocol.Marshal(&initial)
		if err != nil {
			t.Fatal(err)
		}
		for i, action := range actions[:min(len(actions), 8)] {
			switch action % 8 {
			case 0, 1:
				n := uint64(action) + 1
				semanticMetricsCounter.Add(n)
				total += n
				semanticMetricsExpected += n
			case 2:
				reply := semanticRawMetrics(t, w, fmt.Sprintf("malformed-%d", i), &coreprotocol.Empty{})
				if reply.Fault == nil || reply.Fault.Code != "invalid_request" {
					t.Fatal("malformed metrics selection accepted", reply.Fault)
				}
			case 3:
				reply := semanticRawMetrics(t, w, "invalid identity", &q)
				if reply.Fault == nil || reply.Fault.Code != "invalid_request" {
					t.Fatal("invalid metrics identity accepted", reply.Fault)
				}
			case 4:
				owner := w.c.Owner()
				_ = w.currentCarrier().Close()
				s := take() // SDK resumes before this read; no detached owner inferred.
				semanticMetricsShape(t, s, q, total)
				if w.c.Owner() != owner {
					t.Fatal("metrics owner changed across carrier loss")
				}
			case 5:
				owner := w.c.Owner()
				w = newSemanticFuzzWire(t, r, "metrics-semantic")
				if w.c.Owner().ClientID != owner.ClientID || w.c.Owner().Epoch != owner.Epoch+1 {
					t.Fatal("metrics takeover did not fence epoch")
				}
			case 6:
				r.mu.Lock()
				connection := r.clients[w.c.Owner().ClientID]
				r.mu.Unlock()
				owner := w.c.Owner()
				owner.Epoch++
				raw, err := coreprotocol.Marshal(&q)
				if err != nil {
					t.Fatal(err)
				}
				out := <-connection.Dispatch(w.ctx, coreprotocol.Request{ID: fmt.Sprintf("forged-%d", i), Owner: owner, Method: coreprotocol.CoreMetricsMethod, Payload: raw})
				if out.Error == nil || out.Error.Code != "invalid_owner" || len(out.Payload) != 0 {
					t.Fatal("foreign metrics owner observed snapshot")
				}
			case 7:
				// Corrupt a detached reply. Later typed reads must neither inherit
				// the modified metadata nor share this response's point storage.
				s := take()
				for j := range s.Counters {
					if s.Counters[j].Meta.Name == "fuzz.metrics.authored" {
						s.Counters[j].Total = ^uint64(0)
						s.Counters[j].Meta.Name = "corrupted"
						s.Counters[j].Points[0] = ^uint64(0)
					}
				}
			}
			s := take()
			semanticMetricsShape(t, s, q, total)
			still, err := coreprotocol.Marshal(&initial)
			if err != nil || !bytes.Equal(still, held) {
				t.Fatal("retained metrics snapshot mutated after another request")
			}
		}
	})
}
