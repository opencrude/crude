package coreclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

func bulkMessage(t testing.TB, dst *bytes.Buffer, header coreprotocol.BulkHeader, data []byte) {
	t.Helper()
	b, err := coreprotocol.Marshal(&header)
	if err != nil {
		t.Fatal(err)
	}
	if err = corewire.WriteMessage(dst, append(b, data...)); err != nil {
		t.Fatal(err)
	}
}

func TestBulkReceiverRejectsPartialAndInvalid(t *testing.T) {
	want := []byte("complete content")
	hash := sha256.Sum256(want)
	digest := hex.EncodeToString(hash[:])
	var good bytes.Buffer
	bulkMessage(t, &good, coreprotocol.BulkHeader{Seq: 1}, want)
	bulkMessage(t, &good, coreprotocol.BulkHeader{Seq: 2, Offset: int64(len(want)), Final: true, Digest: digest}, nil)
	data, gotDigest, err := receiveBulk(corewire.NewReader(bytes.NewReader(good.Bytes()), coreprotocol.BulkMessageBytes), int64(len(want)))
	if err != nil || !bytes.Equal(want, data) || gotDigest != digest {
		t.Fatal("verified transfer failed", err)
	}
	for cut := 0; cut < good.Len(); cut++ {
		got, _, err := receiveBulk(corewire.NewReader(bytes.NewReader(good.Bytes()[:cut]), coreprotocol.BulkMessageBytes), int64(len(want)))
		if err == nil || got != nil {
			t.Fatalf("partial frame %d published bytes", cut)
		}
	}
	for _, header := range []coreprotocol.BulkHeader{
		{Seq: 1, Offset: 3}, {Seq: 2}, {Seq: 1, Final: true, Digest: digest},
		{Seq: 1, Final: true, Digest: "wrong"}, {Seq: 1, Fault: &coreprotocol.Fault{Code: "failed", Message: "source changed"}},
	} {
		var wire bytes.Buffer
		bulkMessage(t, &wire, header, want)
		got, _, err := receiveBulk(corewire.NewReader(&wire, coreprotocol.BulkMessageBytes), int64(len(want)))
		if err == nil || got != nil {
			t.Fatalf("invalid frame accepted: %+v", header)
		}
	}
}

func TestBulkDownloadCancellationStalledPeer(t *testing.T) {
	f := &fakeCore{t: t}
	f.handle = func(_ coreprotocol.Open, _ []byte, s corewire.Stream) { defer s.Close(); _, _ = io.Copy(io.Discard, s) }
	c := dial(t, f)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	got, _, err := c.DownloadBulk(ctx, "workspace", coreprotocol.FilesRead, &coreprotocol.FilePathRequest{Path: "x"}, nil, coreprotocol.MaxFileRead)
	if err == nil || got != nil {
		t.Fatal("stalled peer did not cancel")
	}
}

func TestBulkUploadLostCommitPreservesExactReceipt(t *testing.T) {
	var commits, aborts atomic.Int32
	a, b := net.Pipe()
	peer, server := corewire.NewMux(a, corewire.CarrierLocal, true), corewire.NewMux(b, corewire.CarrierLocal, false)
	defer peer.Close()
	defer server.Close()
	ready := make(chan struct{})
	close(ready)
	c := &Client{carrier: peer, ready: ready, done: make(chan struct{})}
	handle := func(open coreprotocol.Open, reader *corewire.Reader, s corewire.Stream) {
		defer s.Close()
		if open.Method == coreprotocol.ReferenceUploadAbort {
			aborts.Add(1)
			return
		}
		if open.Method == coreprotocol.ReferenceUploadCommit {
			commits.Add(1)
			_, _ = io.Copy(io.Discard, s)
			return
		}
		if open.Kind != coreprotocol.StreamBulk || s.Class() != corewire.ClassBulk {
			t.Error("upload not on bulk class")
			return
		}
		if _, err := reader.Next(); err != nil {
			return
		}
		metadata, _ := coreprotocol.Marshal(&coreprotocol.ReferenceUploadResult{UploadID: "staged"})
		_ = sendValues(s, &coreprotocol.BulkReady{Size: 3, Metadata: metadata})
		if _, err := reader.Next(); err != nil {
			return
		}
		body, err := reader.Next()
		if err != nil {
			return
		}
		var final coreprotocol.BulkHeader
		if err := coreprotocol.Decode(body, &final); err != nil {
			t.Error(err)
			return
		}
		_ = sendValues(s, &final)
		_, _ = reader.Next() // final ACK
		_ = s.CloseWrite()
	}
	go func() {
		for {
			s, err := server.AcceptStream(t.Context())
			if err != nil {
				return
			}
			go func() {
				reader := corewire.NewReader(s, coreprotocol.BulkMessageBytes)
				body, err := reader.Next()
				if err != nil {
					return
				}
				var open coreprotocol.Open
				if _, err := coreprotocol.DecodePrefix(body, &open); err != nil {
					t.Error(err)
					return
				}
				handle(open, reader, s)
			}()
		}
	}()
	data := []byte("yes")
	hash := sha256.Sum256(data)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := c.UploadReference(ctx, "workspace", coreprotocol.ReferenceUploadCommand{SessionID: "session", Size: 3, Digest: hex.EncodeToString(hash[:])}, bytes.NewReader(data))
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || uncertain.RequestID == "" {
		t.Fatal("lost commit did not expose exact request receipt", err)
	}
	if commits.Load() != 1 || aborts.Load() != 0 {
		t.Fatal("commit replayed or uncertain upload aborted", commits.Load(), aborts.Load())
	}
}

func FuzzBulkReceiver(f *testing.F) {
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, wire []byte) {
		if len(wire) > 1<<20 {
			t.Skip()
		}
		data, _, err := receiveBulk(corewire.NewReader(bytes.NewReader(wire), coreprotocol.BulkMessageBytes), 1024)
		if err != nil && data != nil {
			t.Fatal("failed transfer published partial bytes")
		}
		if len(data) > 1024 {
			t.Fatal("size bound bypass")
		}
	})
}

func BenchmarkBulkVerified8MiB(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 8<<20)
	hash := sha256.Sum256(data)
	var wire bytes.Buffer
	var seq uint64
	for offset := 0; offset < len(data); offset += coreprotocol.BulkChunkBytes {
		seq++
		bulkMessage(b, &wire, coreprotocol.BulkHeader{Seq: seq, Offset: int64(offset)}, data[offset:offset+coreprotocol.BulkChunkBytes])
	}
	bulkMessage(b, &wire, coreprotocol.BulkHeader{Seq: seq + 1, Offset: int64(len(data)), Final: true, Digest: hex.EncodeToString(hash[:])}, nil)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := receiveBulk(corewire.NewReader(bytes.NewReader(wire.Bytes()), coreprotocol.BulkMessageBytes), int64(len(data))); err != nil {
			b.Fatal(err)
		}
	}
}
