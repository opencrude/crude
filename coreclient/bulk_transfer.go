package coreclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

// openBulk never retries a transfer. Interrupted reads publish no bytes;
// interrupted staging publishes no reference. Only the final durable command
// participates in the session's exact-identity receipt protocol.
func (c *Client) openBulk(ctx context.Context, workspaceID, method string, start coreprotocol.BulkStart) (corewire.Stream, *corewire.Reader, error) {
	conn, err := c.current(ctx)
	if err != nil {
		return nil, nil, err
	}
	s, err := conn.OpenStream(ctx, corewire.ClassBulk)
	if err != nil {
		return nil, nil, err
	}
	base := s
	s = &cancelBulkStream{Stream: base, stop: context.AfterFunc(ctx, func() { _ = base.Close() })}
	if err = sendValues(s, &coreprotocol.Open{Kind: coreprotocol.StreamBulk, ID: randomID(), WorkspaceID: workspaceID, Method: method}); err == nil {
		err = sendValues(s, &start)
	}
	if err != nil {
		_ = s.Close()
		return nil, nil, err
	}
	return s, corewire.NewReader(s, coreprotocol.BulkMessageBytes), nil
}

type cancelBulkStream struct {
	corewire.Stream
	stop func() bool
}

func (s *cancelBulkStream) Close() error { s.stop(); return s.Stream.Close() }

func (c *Client) DownloadBulk(ctx context.Context, workspaceID, method string, in coreprotocol.Value, out coreprotocol.Target, limit int64) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	metadata, err := coreprotocol.Marshal(in)
	if err != nil {
		return nil, "", err
	}
	s, reader, err := c.openBulk(ctx, workspaceID, method, coreprotocol.BulkStart{Metadata: metadata})
	if err != nil {
		return nil, "", err
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	body, err := reader.Next()
	if err != nil {
		return nil, "", err
	}
	var ready coreprotocol.BulkReady
	if err = coreprotocol.Decode(body, &ready); err != nil {
		return nil, "", err
	}
	if ready.Fault != nil {
		_ = sendValues(s, &coreprotocol.TopicAck{})
		return nil, "", ready.Fault
	}
	if ready.Size < 0 || ready.Size > limit {
		return nil, "", errors.New("bulk size exceeds domain limit")
	}
	if out != nil {
		if err = coreprotocol.Decode(ready.Metadata, out); err != nil {
			return nil, "", err
		}
	}
	data, digest, err := receiveBulk(reader, ready.Size)
	if err != nil {
		return nil, "", err
	}
	seq := uint64((ready.Size+coreprotocol.BulkChunkBytes-1)/coreprotocol.BulkChunkBytes) + 1
	_ = sendValues(s, &coreprotocol.TopicAck{Seq: seq})
	_ = s.CloseWrite()
	_, _ = reader.Next()
	return data, digest, nil
}

func receiveBulk(reader *corewire.Reader, size int64) ([]byte, string, error) {
	var data []byte
	hash := sha256.New()
	for seq := uint64(1); ; seq++ {
		body, err := reader.Next()
		if err != nil {
			return nil, "", err
		}
		var header coreprotocol.BulkHeader
		chunk, err := coreprotocol.DecodePrefix(body, &header)
		if err != nil {
			return nil, "", err
		}
		if header.Fault != nil {
			return nil, "", header.Fault
		}
		if header.Seq != seq || header.Offset != int64(len(data)) {
			return nil, "", errors.New("invalid bulk sequence or offset")
		}
		if header.Final {
			digest := hex.EncodeToString(hash.Sum(nil))
			if len(chunk) != 0 || int64(len(data)) != size || header.Digest != digest {
				return nil, "", errors.New("bulk length or digest mismatch")
			}
			return data, digest, nil
		}
		if len(chunk) == 0 || len(chunk) > coreprotocol.BulkChunkBytes || int64(len(chunk)) > size-int64(len(data)) || header.Digest != "" {
			return nil, "", errors.New("invalid bulk chunk length")
		}
		_, _ = hash.Write(chunk)
		if len(data)+len(chunk) > cap(data) {
			grown := make([]byte, len(data), min(int(size), max(len(data)+len(chunk), 2*cap(data))))
			copy(grown, data)
			data = grown
		}
		data = append(data, chunk...)
	}
}

// ReadClientFile returns only a fully verified immutable transfer. External
// authority is explicit; a project read never falls back to the external path.
func (c *Client) ReadClientFile(ctx context.Context, workspaceID, path string, external bool) ([]byte, error) {
	method := coreprotocol.FilesRead
	if external {
		method = coreprotocol.ClientFilesReadExternal
	}
	var out coreprotocol.FileChunk
	data, _, err := c.DownloadBulk(ctx, workspaceID, method, &coreprotocol.FilePathRequest{Path: path}, &out, coreprotocol.MaxFileRead)
	if err != nil {
		return nil, err
	}
	if out.Total != int64(len(data)) || out.Path == "" {
		return nil, errors.New("invalid file metadata")
	}
	return data, nil
}

func sendBulkSource(s corewire.Stream, source io.Reader, size int64, digest string) (uint64, error) {
	buffer := make([]byte, coreprotocol.BulkChunkBytes)
	hash := sha256.New()
	var seq uint64
	for offset := int64(0); offset < size; {
		n := int(min(int64(len(buffer)), size-offset))
		if _, err := io.ReadFull(source, buffer[:n]); err != nil {
			return seq, err
		}
		_, _ = hash.Write(buffer[:n])
		seq++
		m := corewire.NewMessage()
		var err error
		m.B, err = (&coreprotocol.BulkHeader{Seq: seq, Offset: offset}).MarshalMsg(m.B)
		if err == nil {
			m.B = append(m.B, buffer[:n]...)
			err = m.Send(s)
		}
		m.Release()
		if err != nil {
			return seq, err
		}
		offset += int64(n)
	}
	var extra [1]byte
	if n, err := source.Read(extra[:]); n != 0 || err != io.EOF {
		return seq, errors.New("upload source exceeds declared size")
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return seq, errors.New("upload source digest mismatch")
	}
	seq++
	return seq, sendValues(s, &coreprotocol.BulkHeader{Seq: seq, Offset: size, Final: true, Digest: digest})
}
