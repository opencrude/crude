package coreclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

// completePayload runs on the awaiting caller, never the multiplexed reader.
// Failure keeps the original effect identity uncertain; only ephemeral reads
// are issued here, and the underlying request is never replayed.
func (c *Client) completePayload(ctx context.Context, request coreprotocol.Request, response coreprotocol.Response) (coreprotocol.Response, error) {
	if response.Transfer == nil {
		return response, nil
	}
	err := completePayload(ctx, &response, func(ctx context.Context, input coreprotocol.PayloadReadRequest, chunk *coreprotocol.PayloadChunk) error {
		return c.Call(ctx, request.WorkspaceID, coreprotocol.PayloadRead, input, chunk)
	}, func(input coreprotocol.PayloadReleaseRequest) {
		releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Call(releaseCtx, request.WorkspaceID, coreprotocol.PayloadRelease, input, nil)
	})
	if err != nil {
		return coreprotocol.Response{}, &UncertainError{RequestID: request.ID, Cause: err}
	}
	return response, nil
}

func completePayload(ctx context.Context, response *coreprotocol.Response, read func(context.Context, coreprotocol.PayloadReadRequest, *coreprotocol.PayloadChunk) error, release func(coreprotocol.PayloadReleaseRequest)) error {
	t := *response.Transfer
	if !validPayloadTransfer(t) || response.Error != nil || len(response.Payload) != 0 {
		return errors.New("invalid payload transfer envelope")
	}
	defer release(coreprotocol.PayloadReleaseRequest{ID: t.ID, Digest: t.Digest})
	data := make(json.RawMessage, int(t.Size))
	for offset := int64(0); offset < t.Size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		var chunk coreprotocol.PayloadChunk
		if err := read(ctx, coreprotocol.PayloadReadRequest{ID: t.ID, Digest: t.Digest, Offset: offset, Limit: coreprotocol.PayloadChunkBytes}, &chunk); err != nil {
			return err
		}
		if chunk.Offset != offset || chunk.Total != t.Size || chunk.Digest != t.Digest || len(chunk.Data) == 0 || len(chunk.Data) > coreprotocol.PayloadChunkBytes || int64(len(chunk.Data)) > t.Size-offset || chunk.Next != offset+int64(len(chunk.Data)) || chunk.EOF != (chunk.Next == t.Size) {
			return errors.New("invalid payload chunk")
		}
		copy(data[offset:], chunk.Data)
		offset = chunk.Next
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != t.Digest || !json.Valid(data) {
		return errors.New("payload digest or JSON mismatch")
	}
	response.Payload = data
	response.Transfer = nil
	return nil
}

func validPayloadTransfer(t corewire.PayloadTransfer) bool {
	id, idErr := hex.DecodeString(t.ID)
	digest, digestErr := hex.DecodeString(t.Digest)
	return idErr == nil && len(id) == 16 && digestErr == nil && len(digest) == sha256.Size && t.Size > 0 && t.Size <= coreprotocol.MaxPayloadBytes
}
