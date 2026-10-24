package coreclient

import (
	"context"
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
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	metadata, err := coreprotocol.Marshal(&in)
	if err != nil {
		return out, err
	}
	s, reader, err := c.openBulk(ctx, workspaceID, coreprotocol.ReferenceUploadBegin, coreprotocol.BulkStart{Size: in.Size, Digest: in.Digest, Metadata: metadata})
	if err != nil {
		return out, err
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	body, err := reader.Next()
	if err != nil {
		return out, err
	}
	var ready coreprotocol.BulkReady
	if err = coreprotocol.Decode(body, &ready); err != nil {
		return out, err
	}
	if ready.Fault != nil {
		_ = sendValues(s, &coreprotocol.TopicAck{})
		return out, ready.Fault
	}
	var begun coreprotocol.ReferenceUploadResult
	if err = coreprotocol.Decode(ready.Metadata, &begun); err != nil {
		return out, err
	}
	if begun.UploadID == "" || ready.Size != in.Size {
		return out, errors.New("invalid upload admission")
	}
	seq, err := sendBulkSource(s, source, in.Size, in.Digest)
	if err != nil {
		return out, err
	}
	body, err = reader.Next()
	if err != nil {
		return out, err
	}
	var final coreprotocol.BulkHeader
	if err = coreprotocol.Decode(body, &final); err != nil {
		return out, err
	}
	if final.Fault != nil {
		_ = sendValues(s, &coreprotocol.TopicAck{})
		return out, final.Fault
	}
	if !final.Final || final.Seq != seq || final.Offset != in.Size || final.Digest != in.Digest {
		return out, errors.New("invalid upload completion")
	}
	_ = sendValues(s, &coreprotocol.TopicAck{Seq: seq})
	_ = s.CloseWrite()
	_, _ = reader.Next()
	// Do not abort on a lost final acknowledgement. CallWith retains this exact
	// commit ID across carrier replacement and exposes it on UncertainError.
	_, err = c.CallWith(ctx, workspaceID, coreprotocol.ReferenceUploadCommit, &coreprotocol.ReferenceUploadCommand{SessionID: in.SessionID, UploadID: begun.UploadID}, &out, CallOptions{ID: randomID()})
	return out, err
}
