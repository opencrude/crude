package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

// A real negotiated carrier, also exposed for deliberately malformed streams.
// Every server and SDK worker is joined before the per-case runtime is closed.
type semanticFuzzWire struct {
	c       *coreclient.Client
	mu      sync.Mutex
	carrier corewire.Conn
	gate    *semanticBulkGate
	wg      sync.WaitGroup
	ctx     context.Context
}

type semanticBulkGate struct{ entered, release chan struct{} }
type semanticGatedConn struct {
	corewire.Conn
	wire *semanticFuzzWire
}
type semanticGatedStream struct {
	corewire.Stream
	gate *semanticBulkGate
	once sync.Once
}

func (s *semanticGatedStream) Write(p []byte) (int, error) {
	n, err := s.Stream.Write(p)
	s.once.Do(func() { close(s.gate.entered); <-s.gate.release })
	return n, err
}

func (c *semanticGatedConn) AcceptStream(ctx context.Context) (corewire.Stream, error) {
	s, err := c.Conn.AcceptStream(ctx)
	if err == nil && s.Class() == corewire.ClassBulk {
		c.wire.mu.Lock()
		gate := c.wire.gate
		c.wire.gate = nil
		c.wire.mu.Unlock()
		if gate != nil {
			s = &semanticGatedStream{Stream: s, gate: gate}
		}
	}
	return s, err
}

func newSemanticFuzzWire(t *testing.T, r *Runtime, id string) *semanticFuzzWire {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	w := &semanticFuzzWire{ctx: ctx}
	dial := func(ctx context.Context) (corewire.Conn, error) {
		a, b := net.Pipe()
		peer := corewire.NewMux(a, corewire.CarrierLocal, true)
		server := &semanticGatedConn{Conn: corewire.NewMux(b, corewire.CarrierLocal, false), wire: w}
		w.mu.Lock()
		w.carrier = peer
		w.mu.Unlock()
		w.wg.Add(1)
		go func() { defer w.wg.Done(); _ = ServeCarrier(w.ctx, r, server, nil) }()
		return peer, nil
	}
	var err error
	w.c, err = coreclient.Dial(ctx, dial, id)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.c.Close(); cancel(); w.wg.Wait() })
	return w
}

func (w *semanticFuzzWire) currentCarrier() corewire.Conn {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.carrier
}

func semanticDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func semanticRead(t *testing.T, reader *corewire.Reader, out coreprotocol.Target) {
	t.Helper()
	body, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if err = coreprotocol.Decode(body, out); err != nil {
		t.Fatal(err)
	}
}

func semanticSend(t *testing.T, s corewire.Stream, values ...coreprotocol.Value) {
	t.Helper()
	if err := send(s, values...); err != nil {
		t.Fatal(err)
	}
}

func semanticUploadStart(t *testing.T, w *semanticFuzzWire, ws, id string, data []byte) (corewire.Stream, *corewire.Reader, string) {
	t.Helper()
	s, err := w.currentCarrier().OpenStream(w.ctx, corewire.ClassBulk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	in := coreprotocol.ReferenceUploadCommand{SessionID: "bulk-session", Purpose: coreprotocol.ReferenceUploadCreate, Size: int64(len(data)), Digest: semanticDigest(data), Create: &coreprotocol.ReferenceCreateRequest{Kind: sessiondb.KindPastedTextFile}}
	metadata, err := coreprotocol.Marshal(&in)
	if err != nil {
		t.Fatal(err)
	}
	semanticSend(t, s, &coreprotocol.Open{Kind: coreprotocol.StreamBulk, ID: id, WorkspaceID: ws, Method: coreprotocol.ReferenceUploadBegin})
	semanticSend(t, s, &coreprotocol.BulkStart{Size: in.Size, Digest: in.Digest, Metadata: metadata})
	reader := corewire.NewReader(s, coreprotocol.BulkMessageBytes)
	var ready coreprotocol.BulkReady
	semanticRead(t, reader, &ready)
	var begun coreprotocol.ReferenceUploadResult
	if ready.Fault != nil || ready.Size != in.Size || coreprotocol.Decode(ready.Metadata, &begun) != nil || begun.UploadID == "" {
		t.Fatal("upload admission", ready.Fault)
	}
	return s, reader, begun.UploadID
}

func semanticChunk(t *testing.T, s corewire.Stream, header coreprotocol.BulkHeader, data []byte) {
	t.Helper()
	raw, err := coreprotocol.Marshal(&header)
	if err == nil {
		err = corewire.WriteMessage(s, append(raw, data...))
	}
	if err != nil {
		t.Fatal(err)
	}
}

// Eight generated actions maximum, 4 KiB authored UTF-8 per upload/file, and
// at most two upload chunks. Expected bytes and SHA-256 are never taken from
// the server's transfer helpers. Reads and reference enumeration check actual
// publication, not just staging counters or matching response encodings.
func FuzzCoreBulkLifecycle(f *testing.F) {
	for _, seed := range [][]byte{{}, {0, 1, 2, 3, 4, 5, 6, 7}, {8, 9, 10, 11, 12, 13, 14, 15}, {7, 0, 6, 1, 5, 2, 4, 3}} {
		f.Add(seed, []byte("authored bulk\n"))
	}
	f.Fuzz(func(t *testing.T, actions, payload []byte) {
		t.Setenv("CRUDE_HOME", t.TempDir())
		root := t.TempDir()
		r, err := New(func(root string) (*workspace.Workspace, error) {
			ws := workspace.NewWorkspace(root, "", "semantic-bulk", types.Settings{}, coreTestLogger{})
			ws.Sessions = append(ws.Sessions, &workspace.Session{SessionID: "bulk-session", RefStore: workspace.NewReferenceStore("bulk-session", t.TempDir(), nil)})
			return ws, nil
		}, filepath.Join(t.TempDir(), "receipts.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := r.Close(); err != nil {
				t.Error(err)
			}
		})
		w := newSemanticFuzzWire(t, r, "bulk-semantic")
		var attach coreprotocol.AttachResult
		if err = w.c.Call(w.ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attach); err != nil {
			t.Fatal(err)
		}
		ws := attach.WorkspaceID
		data := make([]byte, min(len(payload), 4095)+1)
		for i := range data {
			data[i] = ' ' + byte(i%80)
			if i < len(payload) {
				data[i] = ' ' + payload[i]%95
			}
		}
		want := map[string][]byte{}
		check := func() {
			var list coreprotocol.ReferenceListResult
			if err := w.c.Call(w.ctx, ws, coreprotocol.ReferenceList, &coreprotocol.ReferenceCommand{SessionID: "bulk-session"}, &list); err != nil {
				t.Fatal(err)
			}
			if len(list.References) != len(want) {
				t.Fatalf("partial/duplicate publication: got %d want %d", len(list.References), len(want))
			}
			for _, ref := range list.References {
				var result coreprotocol.ReferenceContentResult
				got, digest, err := w.c.DownloadBulk(w.ctx, ws, coreprotocol.ReferenceReadContent, &coreprotocol.ReferenceCommand{SessionID: "bulk-session", ID: ref.Reference.ID}, &result, 4096)
				if err != nil || !bytes.Equal(got, want[ref.Reference.ID]) || digest != semanticDigest(want[ref.Reference.ID]) {
					t.Fatal("published bytes diverged", err)
				}
			}
		}
		for i, action := range actions[:min(len(actions), 8)] {
			id := fmt.Sprintf("bulk-action-%d", i)
			switch action % 8 {
			case 0, 1, 2, 3, 4, 5:
				s, reader, upload := semanticUploadStart(t, w, ws, id, data)
				mode := action % 8
				split := 1 + int(action)%len(data)
				header := coreprotocol.BulkHeader{Seq: 1}
				if mode == 1 {
					header.Offset = 1
				}
				semanticChunk(t, s, header, data[:split])
				if mode == 4 || mode == 5 {
					if mode == 4 {
						_ = s.Close()
					} else {
						owner := w.c.Owner()
						_ = w.currentCarrier().Close()
						// A real command waits for session resume, never restages bytes.
						check()
						if w.c.Owner() != owner {
							t.Fatal("carrier loss replaced bulk owner")
						}
					}
					check()
					continue
				}
				seq, offset := uint64(2), int64(split)
				if mode == 2 {
					semanticChunk(t, s, header, data[:split])
				} else if mode != 1 {
					if split < len(data) {
						semanticChunk(t, s, coreprotocol.BulkHeader{Seq: seq, Offset: offset}, data[split:])
						seq++
						offset = int64(len(data))
					}
					digest := semanticDigest(data)
					if mode == 3 {
						digest = semanticDigest([]byte("wrong"))
					}
					semanticSend(t, s, &coreprotocol.BulkHeader{Seq: seq, Offset: offset, Final: true, Digest: digest})
				}
				var final coreprotocol.BulkHeader
				semanticRead(t, reader, &final)
				if mode != 0 {
					if final.Fault == nil {
						t.Fatal("invalid upload accepted", mode)
					}
					semanticSend(t, s, &coreprotocol.TopicAck{})
					_ = s.CloseWrite()
					_, _ = reader.Next()
					_ = s.Close()
					var ref coreprotocol.ReferenceView
					if err := w.c.Call(w.ctx, ws, coreprotocol.ReferenceUploadCommit, &coreprotocol.ReferenceUploadCommand{SessionID: "bulk-session", UploadID: upload}, &ref); err == nil {
						t.Fatal("failed staging committed")
					}
				} else {
					if final.Fault != nil || !final.Final || final.Digest != semanticDigest(data) || final.Seq != seq || final.Offset != int64(len(data)) {
						t.Fatal("valid staging rejected", final.Fault)
					}
					semanticSend(t, s, &coreprotocol.TopicAck{Seq: seq})
					_ = s.CloseWrite()
					_, _ = reader.Next()
					_ = s.Close()
					check() // fully staged bytes are still not published
					in := &coreprotocol.ReferenceUploadCommand{SessionID: "bulk-session", UploadID: upload}
					var ref, replayed coreprotocol.ReferenceView
					opts := coreclient.CallOptions{ID: id + "-commit"}
					if _, err := w.c.CallWith(w.ctx, ws, coreprotocol.ReferenceUploadCommit, in, &ref, opts); err != nil {
						t.Fatal(err)
					}
					want[ref.Reference.ID] = bytes.Clone(data)
					if _, err := w.c.CallWith(w.ctx, ws, coreprotocol.ReferenceUploadCommit, in, &replayed, opts); err != nil || replayed.Reference.ID != ref.Reference.ID {
						t.Fatal("commit receipt replay changed identity", err)
					}
					if err := w.c.Call(w.ctx, ws, coreprotocol.ReferenceUploadCommit, in, &replayed); err == nil {
						t.Fatal("consumed upload committed under fresh identity")
					}
				}
			case 6:
				path := filepath.Join(root, "source")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				got, err := w.c.ReadClientFile(w.ctx, ws, "source", false)
				if err != nil || !bytes.Equal(got, data) {
					t.Fatal("file transfer", err)
				}
				ctx, cancel := context.WithCancel(w.ctx)
				cancel()
				if got, err := w.c.ReadClientFile(ctx, ws, "source", false); err == nil || got != nil {
					t.Fatal("cancelled read published bytes")
				}
				if got, err := w.c.ReadClientFile(w.ctx, ws, "../outside", false); err == nil || got != nil {
					t.Fatal("read escaped project")
				}
			case 7:
				path := filepath.Join(root, "source")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				gate := &semanticBulkGate{entered: make(chan struct{}), release: make(chan struct{})}
				w.mu.Lock()
				w.gate = gate
				w.mu.Unlock()
				type result struct {
					data []byte
					err  error
				}
				done := make(chan result, 1)
				go func() { b, e := w.c.ReadClientFile(w.ctx, ws, "source", false); done <- result{b, e} }()
				select {
				case <-gate.entered:
				case <-w.ctx.Done():
					close(gate.release)
					t.Fatal("source read not admitted")
				}
				// The checked descriptor is already open; replacement has identical
				// bytes/length but a distinct inode, independent of timestamp precision.
				err := os.Rename(path, fmt.Sprintf("%s.old-%d", path, i))
				if err == nil {
					err = os.WriteFile(path, data, 0600)
				}
				close(gate.release)
				out := <-done
				if err != nil {
					t.Fatal(err)
				}
				if out.err == nil || out.data != nil {
					t.Fatal("changed source published unverified bytes")
				}
			}
			check()
		}
	})
}
