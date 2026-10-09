package core

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

func transferRequest(t *testing.T, method string, input any) corewire.Frame {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return corewire.Frame{Version: 1, Kind: corewire.KindRequest, RequestID: "chunk", ClientID: "shell", Epoch: 3, WorkspaceID: "workspace", Method: method, Payload: b}
}

func TestPayloadTransferDocumentAndBinding(t *testing.T) {
	s := newPayloadTransfers()
	defer s.Close()
	// Real document workload: both live and saved 16 MiB byte snapshots become
	// base64 JSON, much larger than the encoded frame ceiling.
	document := bytes.Repeat([]byte("a"), 16<<20)
	data, err := json.Marshal(struct{ Text, Saved []byte }{document, document})
	if err != nil {
		t.Fatal(err)
	}
	original := corewire.Frame{Version: 1, Kind: corewire.KindResponse, RequestID: "open", ClientID: "shell", Epoch: 3, WorkspaceID: "workspace", Method: "document.open", Revision: 42, Payload: data}
	f := s.WrapFrame(original)
	if f.Transfer == nil || f.Error != nil || len(f.Payload) != 0 || f.Method != original.Method || f.RequestID != "open" || f.Revision != 42 {
		t.Fatalf("invalid wrapped response: %+v", f)
	}
	b, _ := json.Marshal(f)
	if len(b) > corewire.MaxFrameSize {
		t.Fatal("descriptor exceeds frame cap")
	}
	var got bytes.Buffer
	for offset := int64(0); offset < f.Transfer.Size; {
		r := transferRequest(t, coreprotocol.PayloadRead, coreprotocol.PayloadReadRequest{ID: f.Transfer.ID, Digest: f.Transfer.Digest, Offset: offset, Limit: coreprotocol.PayloadChunkBytes})
		chunkFrame, handled := s.ReadFrame(r)
		if !handled || chunkFrame.Error != nil || chunkFrame.RequestID != r.RequestID || chunkFrame.Method != r.Method || chunkFrame.WorkspaceID != r.WorkspaceID {
			t.Fatal("chunk binding lost")
		}
		encoded, _ := json.Marshal(chunkFrame)
		if len(encoded) > corewire.MaxFrameSize {
			t.Fatal("chunk exceeds frame cap")
		}
		var chunk coreprotocol.PayloadChunk
		if err := json.Unmarshal(chunkFrame.Payload, &chunk); err != nil {
			t.Fatal(err)
		}
		if chunk.Offset != offset || chunk.Next != offset+int64(len(chunk.Data)) || chunk.Digest != f.Transfer.Digest || chunk.Total != int64(len(data)) || chunk.EOF != (chunk.Next == chunk.Total) {
			t.Fatal("invalid chunk metadata")
		}
		got.Write(chunk.Data)
		offset = chunk.Next
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatal("document snapshot changed")
	}
	if !bytes.Equal(original.Payload, data) {
		t.Fatal("authoritative response changed")
	}
}

func TestPayloadTransferIsolationQuotasAndRelease(t *testing.T) {
	s := newPayloadTransfers(1024)
	defer s.Close()
	other := newPayloadTransfers()
	defer other.Close()
	data := json.RawMessage(`"` + string(bytes.Repeat([]byte("x"), 2048)) + `"`)
	frame := corewire.Frame{Version: 1, Kind: corewire.KindResponse, Payload: data}
	f := s.WrapFrame(frame)
	if f.Transfer == nil {
		t.Fatal("missing transfer")
	}
	read := coreprotocol.PayloadReadRequest{ID: f.Transfer.ID, Digest: f.Transfer.Digest, Limit: 32}
	r, _ := other.ReadFrame(transferRequest(t, coreprotocol.PayloadRead, read))
	if r.Error == nil {
		t.Fatal("cross-connection token authorized")
	}
	read.Digest = "wrong"
	r, _ = s.ReadFrame(transferRequest(t, coreprotocol.PayloadRead, read))
	if r.Error == nil {
		t.Fatal("wrong digest authorized")
	}
	for i := 1; i < payloadTransferCount; i++ {
		if s.WrapFrame(frame).Transfer == nil {
			t.Fatal("quota reached early")
		}
	}
	if s.WrapFrame(frame).Error == nil {
		t.Fatal("count quota not enforced")
	}
	release := transferRequest(t, coreprotocol.PayloadRelease, coreprotocol.PayloadReleaseRequest{ID: f.Transfer.ID, Digest: f.Transfer.Digest})
	r, _ = s.ReadFrame(release)
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	read.Digest = f.Transfer.Digest
	r, _ = s.ReadFrame(transferRequest(t, coreprotocol.PayloadRead, read))
	if r.Error == nil {
		t.Fatal("released token still authorized")
	}
	if s.WrapFrame(frame).Transfer == nil {
		t.Fatal("release failed to free quota")
	}
	remaining := s.WrapFrame(frame)
	if remaining.Error == nil {
		t.Fatal("expected full count quota")
	}
	for id, p := range s.entries {
		p.expires = time.Now().Add(-time.Second)
		r, _ = s.ReadFrame(transferRequest(t, coreprotocol.PayloadRead, coreprotocol.PayloadReadRequest{ID: id, Digest: p.digest, Limit: 1}))
		if r.Error == nil {
			t.Fatal("expired transfer still authorized")
		}
		break
	}
	s.Close()
	if s.bytes != 0 || len(s.entries) != 0 || s.WrapFrame(frame).Error == nil {
		t.Fatal("close did not clear and reject")
	}
}

func TestPayloadTransferMalformedAndTotalQuota(t *testing.T) {
	s := newPayloadTransfers(1024)
	defer s.Close()
	frame := corewire.Frame{Version: 1, Kind: corewire.KindResponse, Payload: json.RawMessage(`"` + string(bytes.Repeat([]byte("a"), 2048)) + `"`)}
	f := s.WrapFrame(frame)
	for _, read := range []coreprotocol.PayloadReadRequest{
		{ID: f.Transfer.ID, Digest: f.Transfer.Digest, Offset: -1, Limit: 1},
		{ID: f.Transfer.ID, Digest: f.Transfer.Digest, Offset: f.Transfer.Size, Limit: 1},
		{ID: f.Transfer.ID, Digest: f.Transfer.Digest, Limit: coreprotocol.PayloadChunkBytes + 1},
		{ID: f.Transfer.ID, Digest: f.Transfer.Digest, Limit: 0},
	} {
		if r, _ := s.ReadFrame(transferRequest(t, coreprotocol.PayloadRead, read)); r.Error == nil {
			t.Fatal("invalid read accepted")
		}
	}
	// Account the retained quota independently from the count bound without a
	// 128 MiB test allocation; one further genuine payload must be rejected.
	s.bytes = payloadTransferTotal
	if r := s.WrapFrame(frame); r.Error == nil || r.Transfer != nil || len(r.Payload) != 0 {
		t.Fatal("total-byte quota not enforced with bounded fault")
	}
}
