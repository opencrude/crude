package coreclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"crude/coreprotocol"
	"crude/corewire"
)

func payloadResponse(data []byte) coreprotocol.Response {
	d := sha256.Sum256(data)
	return coreprotocol.Response{ID: "original-effect", Transfer: &corewire.PayloadTransfer{ID: "0123456789abcdef0123456789abcdef", Digest: hex.EncodeToString(d[:]), Size: int64(len(data))}}
}

func TestCompletePayloadPreservesExactJSON(t *testing.T) {
	data := []byte(`{"text":"` + string(bytes.Repeat([]byte("x"), 9<<20)) + `"}`)
	r := payloadResponse(data)
	transferID := r.Transfer.ID
	released := false
	reads := 0
	err := completePayload(context.Background(), &r, func(_ context.Context, read coreprotocol.PayloadReadRequest, chunk *coreprotocol.PayloadChunk) error {
		reads++
		end := read.Offset + int64(read.Limit)
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		*chunk = coreprotocol.PayloadChunk{Data: data[read.Offset:end], Offset: read.Offset, Next: end, Total: int64(len(data)), Digest: r.Transfer.Digest, EOF: end == int64(len(data))}
		return nil
	}, func(release coreprotocol.PayloadReleaseRequest) { released = release.ID == transferID })
	if err != nil || !released || reads < 2 || r.Transfer != nil || !bytes.Equal(r.Payload, data) {
		t.Fatalf("completion failed: %v", err)
	}
}

func TestCompletePayloadRejectsMalformedChunks(t *testing.T) {
	data := []byte(`{"text":"payload"}`)
	for _, tc := range []struct {
		name   string
		mutate func(*coreprotocol.PayloadChunk)
	}{
		{"offset", func(c *coreprotocol.PayloadChunk) { c.Offset++ }},
		{"next", func(c *coreprotocol.PayloadChunk) { c.Next-- }},
		{"total", func(c *coreprotocol.PayloadChunk) { c.Total++ }},
		{"digest", func(c *coreprotocol.PayloadChunk) { c.Digest = "wrong" }},
		{"eof", func(c *coreprotocol.PayloadChunk) { c.EOF = false }},
		{"empty", func(c *coreprotocol.PayloadChunk) { c.Data = nil }},
		{"oversize", func(c *coreprotocol.PayloadChunk) { c.Data = make([]byte, coreprotocol.PayloadChunkBytes+1) }},
		{"corrupt", func(c *coreprotocol.PayloadChunk) { c.Data = bytes.Repeat([]byte("a"), len(data)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := payloadResponse(data)
			released := false
			err := completePayload(context.Background(), &r, func(_ context.Context, _ coreprotocol.PayloadReadRequest, c *coreprotocol.PayloadChunk) error {
				*c = coreprotocol.PayloadChunk{Data: data, Offset: 0, Next: int64(len(data)), Total: int64(len(data)), Digest: r.Transfer.Digest, EOF: true}
				tc.mutate(c)
				return nil
			}, func(coreprotocol.PayloadReleaseRequest) { released = true })
			if err == nil || !released || len(r.Payload) != 0 {
				t.Fatal("malformed chunk installed")
			}
		})
	}
}

func TestCompletePayloadCancellationAndReadFailure(t *testing.T) {
	for _, failure := range []error{context.Canceled, errors.New("lost response")} {
		r := payloadResponse([]byte(`{}`))
		ctx, cancel := context.WithCancel(context.Background())
		if failure == context.Canceled {
			cancel()
		}
		reads := 0
		err := completePayload(ctx, &r, func(context.Context, coreprotocol.PayloadReadRequest, *coreprotocol.PayloadChunk) error {
			reads++
			return failure
		}, func(coreprotocol.PayloadReleaseRequest) {})
		cancel()
		if !errors.Is(err, failure) || (failure == context.Canceled && reads != 0) {
			t.Fatal("failed transfer replayed or swallowed failure")
		}
	}
	// Invalid descriptors are rejected before allocation or any RPC.
	r := payloadResponse(json.RawMessage(`{}`))
	r.Transfer.Size = coreprotocol.MaxPayloadBytes + 1
	if completePayload(context.Background(), &r, func(context.Context, coreprotocol.PayloadReadRequest, *coreprotocol.PayloadChunk) error {
		t.Fatal("invalid descriptor read")
		return nil
	}, func(coreprotocol.PayloadReleaseRequest) {}) == nil {
		t.Fatal("oversize descriptor accepted")
	}
}

func TestPayloadTransferCallRequestWireAndUncertainty(t *testing.T) {
	for _, loseChunk := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "lost_chunk"}[loseChunk], func(t *testing.T) {
			server, client := net.Pipe()
			serverDone := make(chan error, 1)
			data := []byte(`{"dense":"` + string(bytes.Repeat([]byte("x"), 9<<20)) + `"}`)
			descriptor := payloadResponse(data).Transfer
			go func() {
				f := corewire.NewFramer(context.Background(), server)
				defer f.Close()
				id, _, err := f.NegotiateServer(corewire.MaxFrameSize)
				if err != nil {
					serverDone <- err
					return
				}
				owner := coreprotocol.Owner{ClientID: id, Epoch: 1}
				ready, _ := json.Marshal(owner)
				if err = f.Write(corewire.Frame{Version: 1, Kind: corewire.KindEvent, ClientID: id, Epoch: 1, Method: "connection.ready", Payload: ready}); err != nil {
					serverDone <- err
					return
				}
				original, err := f.Read()
				if err != nil {
					serverDone <- err
					return
				}
				response := original
				response.Kind = corewire.KindResponse
				response.Payload = nil
				response.Transfer = descriptor
				if err = f.Write(response); err != nil {
					serverDone <- err
					return
				}
				for {
					request, e := f.Read()
					if e != nil {
						serverDone <- e
						return
					}
					if loseChunk {
						serverDone <- nil
						return
					}
					response = request
					response.Kind = corewire.KindResponse
					if request.Method == coreprotocol.PayloadRelease {
						response.Payload = json.RawMessage(`{}`)
						serverDone <- f.Write(response)
						return
					}
					if request.Method != coreprotocol.PayloadRead {
						serverDone <- errors.New("underlying mutation replayed")
						return
					}
					var read coreprotocol.PayloadReadRequest
					if e = json.Unmarshal(request.Payload, &read); e != nil {
						serverDone <- e
						return
					}
					end := read.Offset + int64(read.Limit)
					if end > int64(len(data)) {
						end = int64(len(data))
					}
					response.Payload, _ = json.Marshal(coreprotocol.PayloadChunk{Data: data[read.Offset:end], Offset: read.Offset, Next: end, Total: int64(len(data)), Digest: descriptor.Digest, EOF: end == int64(len(data))})
					if e = f.Write(response); e != nil {
						serverDone <- e
						return
					}
				}
			}()
			c, err := Dial(context.Background(), client, "shell")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r, err := c.CallRequest(context.Background(), coreprotocol.Request{ID: "exact-effect", Owner: c.Owner(), Method: "mutation.snapshot", WorkspaceID: "workspace", Payload: json.RawMessage(`{}`)})
			if loseChunk {
				var uncertain *UncertainError
				if !errors.As(err, &uncertain) || uncertain.RequestID != "exact-effect" {
					t.Fatalf("original identity lost: %v", err)
				}
			} else if err != nil || !bytes.Equal(r.Payload, data) || r.Transfer != nil {
				t.Fatalf("wire completion failed: %v", err)
			}
			if err = <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}
