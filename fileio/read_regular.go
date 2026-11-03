package fileio

import (
	"context"
	"errors"
	"io"
	"os"
)

// ErrNotRegular means the input is a link, directory or special file.
var ErrNotRegular = errors.New("fileio: input is not a regular non-link file")

// ErrReadLimit means a regular input exceeds the caller's byte bound.
var ErrReadLimit = errors.New("fileio: regular input exceeds read bound")

// ReadRegularNoFollow reads at most maxBytes from a regular file. The native
// opener never follows the final path component and never waits for a FIFO
// writer, including when that component is replaced after Lstat. It verifies
// the opened identity before reading. Parent-directory traversal is the caller's
// responsibility; this is not a filesystem sandbox.
//
// Cancellation is checked before admission and between bounded reads. Like
// ordinary directory traversal, a kernel call on a stalled filesystem cannot
// be preempted by context cancellation.
func ReadRegularNoFollow(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxBytes < 0 || maxBytes == int64(^uint64(0)>>1) {
		return nil, ErrReadLimit
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	if before.Size() > maxBytes {
		return nil, ErrReadLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := openRegularNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	if !os.SameFile(before, opened) {
		return nil, errors.New("fileio: regular input changed while opening")
	}
	if opened.Size() > maxBytes {
		return nil, ErrReadLimit
	}
	data, err := io.ReadAll(io.LimitReader(regularContextReader{ctx, f}, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrReadLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

type regularContextReader struct {
	ctx  context.Context
	file *os.File
}

func (r regularContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(p[:min(len(p), 32<<10)])
}
