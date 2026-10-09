package coreclient

import (
	"context"
	"crude/coreprotocol"
	"errors"
)

// ReadBoundedClientFile assembles immutable checked chunks. Invalid responses
// return no partial bytes, including changed content, non-progress and overflow.
func ReadBoundedClientFile(ctx context.Context, path string, read func(context.Context, coreprotocol.FileReadRequest) (coreprotocol.FileChunk, error)) ([]byte, error) {
	in := coreprotocol.FileReadRequest{Path: path, Limit: coreprotocol.MaxFileChunk}
	var data []byte
	var total int64 = -1
	canonical := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunk, err := read(ctx, in)
		if err != nil {
			return nil, err
		}
		if chunk.Total < 0 || chunk.Total > coreprotocol.MaxFileRead || chunk.Offset != in.Offset ||
			len(chunk.Data) > coreprotocol.MaxFileChunk || chunk.NextOffset != in.Offset+int64(len(chunk.Data)) ||
			chunk.NextOffset > chunk.Total || chunk.Digest == "" || chunk.Path == "" ||
			chunk.EOF != (chunk.NextOffset == chunk.Total) || (!chunk.EOF && len(chunk.Data) == 0) {
			return nil, errors.New("invalid file chunk")
		}
		if total < 0 {
			total, canonical, in.Digest = chunk.Total, chunk.Path, chunk.Digest
		} else if chunk.Total != total || chunk.Path != canonical || chunk.Digest != in.Digest {
			return nil, errors.New("file changed between chunks")
		}
		data = append(data, chunk.Data...)
		if chunk.EOF {
			return data, nil
		}
		in.Offset = chunk.NextOffset
	}
}
