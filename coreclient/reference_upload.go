package coreclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"crude/coreprotocol"
)

// UploadReference streams already-sized, SHA-256 identified content. Invoke on
// a background command; creation metadata contains no bytes, replacement uses
// only the original download digest and exact reference revision.
func (c *Client) UploadReference(ctx context.Context, workspaceID string, in coreprotocol.ReferenceUploadCommand, source io.Reader) (coreprotocol.ReferenceView, error) {
	var out coreprotocol.ReferenceView
	if in.Size < 0 || in.Size > coreprotocol.ReferenceMaxBytes || source == nil {
		return out, errors.New("invalid upload source")
	}
	var begun coreprotocol.ReferenceUploadResult
	if err := c.Call(ctx, workspaceID, coreprotocol.ReferenceUploadBegin, in, &begun); err != nil {
		return out, err
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = c.Call(cleanup, workspaceID, coreprotocol.ReferenceUploadAbort, coreprotocol.ReferenceUploadCommand{SessionID: in.SessionID, UploadID: begun.UploadID}, nil)
		}
	}()
	buffer := make([]byte, coreprotocol.ReferenceChunkBytes)
	hash := sha256.New()
	for offset := int64(0); offset < in.Size; {
		n := int(min(int64(len(buffer)), in.Size-offset))
		if _, err := io.ReadFull(source, buffer[:n]); err != nil {
			return out, err
		}
		_, _ = hash.Write(buffer[:n])
		var progress coreprotocol.ReferenceUploadResult
		if err := c.Call(ctx, workspaceID, coreprotocol.ReferenceUploadChunk, coreprotocol.ReferenceUploadCommand{SessionID: in.SessionID, UploadID: begun.UploadID, Offset: offset, Data: buffer[:n]}, &progress); err != nil {
			return out, err
		}
		offset += int64(n)
		if progress.UploadID != begun.UploadID || progress.Next != offset {
			return out, errors.New("invalid upload acknowledgement")
		}
	}
	var extra [1]byte
	if n, err := source.Read(extra[:]); n != 0 || err != io.EOF {
		return out, errors.New("upload source exceeds declared size")
	}
	if hex.EncodeToString(hash.Sum(nil)) != in.Digest {
		return out, errors.New("upload source digest mismatch")
	}
	err := c.Call(ctx, workspaceID, coreprotocol.ReferenceUploadCommit, coreprotocol.ReferenceUploadCommand{SessionID: in.SessionID, UploadID: begun.UploadID}, &out)
	committed = err == nil
	return out, err
}
