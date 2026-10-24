package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/workspace"
)

// A transfer has a bounded lifetime even if a healthy carrier's peer stops
// consuming this particular stream. Carrier credit bounds queued bytes.
const bulkTimeout = 2 * time.Minute

func (c *Connection) serveBulk(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	ctx, cancel := context.WithTimeout(ctx, bulkTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	fail := func(err error) {
		if send(s, &coreprotocol.BulkReady{Fault: &coreprotocol.Fault{Code: "bulk_failed", Message: err.Error()}}) == nil {
			awaitBulkAck(reader, 0)
		}
		_ = s.CloseWrite()
	}
	if s.Class() != corewire.ClassBulk || !validID(open.ID) {
		fail(errors.New("invalid bulk stream"))
		return
	}
	body, err := reader.Next()
	if err != nil {
		return
	}
	var start coreprotocol.BulkStart
	if err = coreprotocol.Decode(body, &start); err != nil || len(start.Metadata) > 64<<10 {
		fail(errors.New("invalid bulk metadata"))
		return
	}
	c.mu.Lock()
	a := c.attached[open.WorkspaceID]
	c.mu.Unlock()
	if a == nil {
		fail(errors.New("workspace is not attached"))
		return
	}
	var root string
	var store workspace.SessionReferences
	admitted := false
	var sessionID string
	switch open.Method {
	case coreprotocol.ReferenceUploadBegin:
		var in coreprotocol.ReferenceUploadCommand
		if err = coreprotocol.Decode(start.Metadata, &in); err != nil {
			fail(err)
			return
		}
		sessionID = in.SessionID
	case coreprotocol.ReferenceReadContent, coreprotocol.ReferenceReadMedia:
		var in coreprotocol.ReferenceCommand
		if err = coreprotocol.Decode(start.Metadata, &in); err != nil {
			fail(err)
			return
		}
		sessionID = in.SessionID
	}
	// Admission transfers one lifecycle pin. Once queued, join the actor's
	// answer even if the caller cancels, so a late pin can never be leaked.
	if err = a.inspect(context.WithoutCancel(ctx), func() {
		if !a.validClient(c) || a.retiring || a.inFlight >= 16 {
			return
		}
		a.inFlight++
		admitted = true
		root = a.ws.RootPath
		for _, owner := range a.ws.Sessions {
			if owner.SessionID == sessionID {
				store = owner.RefStore
				break
			}
		}
	}); err != nil {
		fail(err)
		return
	}
	if !admitted {
		fail(errors.New("bulk owner detached or busy"))
		return
	}
	defer func() {
		a.jobs <- actorJobResult{admitted: true, command: actorCommand{result: make(chan coreprotocol.Response, 1)}, apply: func() coreprotocol.Response { return coreprotocol.Response{} }}
	}()
	stopOwner := context.AfterFunc(a.ctx, cancel)
	defer stopOwner()
	if err = ctx.Err(); err != nil {
		fail(err)
		return
	}
	if open.Method == coreprotocol.ReferenceUploadBegin {
		if store == nil {
			fail(errors.New("exact session unavailable"))
			return
		}
		c.receiveBulkUpload(ctx, s, reader, open, start, store)
		return
	}
	var source io.ReadCloser
	var size int64
	var metadata coreprotocol.Value
	verify := func() error { return nil }
	switch open.Method {
	case coreprotocol.FilesRead, coreprotocol.ClientFilesReadExternal:
		var in coreprotocol.FilePathRequest
		if err = coreprotocol.Decode(start.Metadata, &in); err != nil {
			break
		}
		if open.Method == coreprotocol.ClientFilesReadExternal {
			in.Path, err = externalReadPath(in.Path)
			if err != nil {
				break
			}
			root = filepath.VolumeName(in.Path) + string(filepath.Separator)
		}
		var file *checkedFile
		file, err = openCheckedFile(root, in.Path, coreprotocol.MaxFileRead)
		if err == nil {
			source, size, verify = file, file.before.Size(), file.verify
			metadata = &coreprotocol.FileChunk{Path: file.path, Total: size}
		}
	case coreprotocol.ReferenceReadContent, coreprotocol.ReferenceReadMedia:
		var in coreprotocol.ReferenceCommand
		if err = coreprotocol.Decode(start.Metadata, &in); err != nil {
			break
		}
		var result coreprotocol.ReferenceContentResult
		result, err = readBulkReference(store, open.Method, in)
		if err == nil {
			source = io.NopCloser(bytes.NewReader(result.Data))
			size = int64(len(result.Data))
			result.Data = nil
			metadata = &result
		}
	case coreprotocol.HistoryReadMedia:
		var in coreprotocol.HistoryCommand
		if err = coreprotocol.Decode(start.Metadata, &in); err != nil {
			break
		}
		var result coreprotocol.ReferenceContentResult
		result, err = c.readBulkHistory(in)
		if err == nil {
			source = io.NopCloser(bytes.NewReader(result.Data))
			size = int64(len(result.Data))
			result.Data = nil
			metadata = &result
		}
	default:
		err = errors.New("unknown bulk method")
	}
	if err != nil {
		fail(err)
		return
	}
	defer source.Close()
	raw, err := coreprotocol.Marshal(metadata)
	if err != nil {
		fail(err)
		return
	}
	if send(s, &coreprotocol.BulkReady{Size: size, Metadata: raw}) != nil {
		return
	}
	hash := sha256.New()
	buffer := make([]byte, coreprotocol.BulkChunkBytes)
	var offset int64
	var seq uint64
	for offset < size {
		if err = ctx.Err(); err != nil {
			break
		}
		n := int(min(int64(len(buffer)), size-offset))
		if _, err = io.ReadFull(source, buffer[:n]); err != nil {
			break
		}
		_, _ = hash.Write(buffer[:n])
		seq++
		m := corewire.NewMessage()
		m.B, err = (&coreprotocol.BulkHeader{Seq: seq, Offset: offset}).MarshalMsg(m.B)
		if err == nil {
			m.B = append(m.B, buffer[:n]...)
			err = m.Send(s)
		}
		m.Release()
		if err != nil {
			return
		}
		offset += int64(n)
	}
	if err == nil {
		var extra [1]byte
		n, e := source.Read(extra[:])
		if n != 0 || e != io.EOF {
			err = errors.New("source changed length")
		}
	}
	if err == nil {
		err = verify()
	}
	final := coreprotocol.BulkHeader{Seq: seq + 1, Offset: offset, Final: true, Digest: hex.EncodeToString(hash.Sum(nil))}
	if err != nil {
		final.Fault = &coreprotocol.Fault{Code: "bulk_failed", Message: err.Error()}
	}
	if send(s, &final) == nil {
		awaitBulkAck(reader, final.Seq)
	}
	_ = s.CloseWrite()
}

func awaitBulkAck(reader *corewire.Reader, seq uint64) {
	body, err := reader.Next()
	var ack coreprotocol.TopicAck
	if err != nil || coreprotocol.Decode(body, &ack) != nil || ack.Seq != seq {
		return
	}
}

type checkedFile struct {
	*os.File
	root, path string
	before     os.FileInfo
}

func openCheckedFile(root, path string, limit int64) (*checkedFile, error) {
	path, err := workspaceFilePath(root, path, false)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, errors.New("read requires a regular file within size bound")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		_ = f.Close()
		return nil, errors.New("file identity changed")
	}
	return &checkedFile{File: f, root: root, path: path, before: before}, nil
}

func (f *checkedFile) verify() error {
	after, err := f.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(f.path)
	if err != nil {
		return err
	}
	if _, err = workspaceFilePath(f.root, f.path, false); err != nil {
		return err
	}
	if !os.SameFile(f.before, current) || !os.SameFile(f.before, after) || current.Size() != f.before.Size() || !current.ModTime().Equal(f.before.ModTime()) || current.Mode() != f.before.Mode() || after.Size() != f.before.Size() || !after.ModTime().Equal(f.before.ModTime()) || after.Mode() != f.before.Mode() {
		return errors.New("file changed during read")
	}
	return nil
}

func readBulkReference(store workspace.SessionReferences, method string, in coreprotocol.ReferenceCommand) (coreprotocol.ReferenceContentResult, error) {
	referenceCommandMu.Lock()
	defer referenceCommandMu.Unlock()
	var out coreprotocol.ReferenceContentResult
	if store == nil || in.Offset != 0 || in.Digest != "" {
		return out, errors.New("invalid reference transfer")
	}
	ref, ok := store.GetReference(in.ID)
	if !ok || !ref.Active() || ref.SessionID != in.SessionID || in.Revision != "" && in.Revision != entityRevision(ref) {
		return out, errors.New("reference is missing or changed")
	}
	out.View = referenceView(ref)
	// Content travels once in the verified byte body, not a second time inside
	// method metadata. Revision still fences the complete authoritative entity.
	out.View.Reference.Content = ""
	var err error
	if method == coreprotocol.ReferenceReadMedia {
		out.ReadOnly = true
		out.Data, err = readCheckedAttachment(ref.FilePath)
	} else {
		var checked *checkedFile
		if ref.FilePath != "" {
			checked, err = openCheckedFile(filepath.VolumeName(ref.FilePath)+string(filepath.Separator), ref.FilePath, coreprotocol.ReferenceMaxBytes)
			if err != nil {
				return out, err
			}
			defer checked.Close()
		}
		var text string
		text, out.ReadOnly, err = store.ReadReferenceContent(ref.ID)
		out.Data = []byte(text)
		if err == nil && checked != nil {
			err = checked.verify()
		}
	}
	if len(out.Data) > coreprotocol.ReferenceMaxBytes {
		return out, errors.New("reference exceeds size bound")
	}
	out.Total, out.Next = int64(len(out.Data)), int64(len(out.Data))
	return out, err
}

func (c *Connection) readBulkHistory(in coreprotocol.HistoryCommand) (coreprotocol.ReferenceContentResult, error) {
	referenceCommandMu.Lock()
	defer referenceCommandMu.Unlock()
	var out coreprotocol.ReferenceContentResult
	if c.runtime.history == nil || in.Offset != 0 || in.Digest != "" {
		return out, errors.New("invalid history transfer")
	}
	for _, entry := range c.runtime.history.Entries() {
		if entry.ID != in.EntryID {
			continue
		}
		if in.AttachmentIndex < 0 || in.AttachmentIndex >= len(entry.Attachments) {
			break
		}
		item := entry.Attachments[in.AttachmentIndex]
		if item.Unavailable || item.Reference.FilePath == "" {
			break
		}
		data, err := readCheckedAttachment(item.Reference.FilePath)
		return coreprotocol.ReferenceContentResult{View: referenceView(item.Reference), ReadOnly: true, Data: data, Total: int64(len(data)), Next: int64(len(data))}, err
	}
	return out, errors.New("exact history media unavailable")
}

func readCheckedAttachment(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("attachment requires an absolute path")
	}
	f, err := openCheckedFile(filepath.VolumeName(path)+string(filepath.Separator), path, coreprotocol.ReferenceMaxBytes)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, coreprotocol.ReferenceMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != f.before.Size() {
		return nil, errors.New("attachment changed length")
	}
	if err = f.verify(); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *Connection) receiveBulkUpload(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open, start coreprotocol.BulkStart, store workspace.SessionReferences) {
	var in coreprotocol.ReferenceUploadCommand
	_ = coreprotocol.Decode(start.Metadata, &in)
	m := c.runtime.uploads
	readySent := false
	fail := func(err error) {
		fault := &coreprotocol.Fault{Code: "upload_failed", Message: err.Error()}
		var value coreprotocol.Value = &coreprotocol.BulkReady{Fault: fault}
		if readySent {
			value = &coreprotocol.BulkHeader{Final: true, Fault: fault}
		}
		if send(s, value) == nil {
			awaitBulkAck(reader, 0)
		}
		_ = s.CloseWrite()
	}
	if in.Size != start.Size || in.Digest != start.Digest {
		fail(errors.New("upload declaration mismatch"))
		return
	}
	value, err := m.run(ctx, c.Owner, open.WorkspaceID, store, coreprotocol.ReferenceUploadBegin, in)
	if err != nil {
		fail(err)
		return
	}
	id := value.(coreprotocol.ReferenceUploadResult).UploadID
	staged := false
	defer func() {
		if !staged {
			_, _ = m.run(context.Background(), c.Owner, open.WorkspaceID, store, coreprotocol.ReferenceUploadAbort, coreprotocol.ReferenceUploadCommand{SessionID: in.SessionID, UploadID: id})
		}
	}()
	metadata, _ := coreprotocol.Marshal(&coreprotocol.ReferenceUploadResult{UploadID: id})
	if send(s, &coreprotocol.BulkReady{Size: in.Size, Metadata: metadata}) != nil {
		return
	}
	readySent = true
	hash := sha256.New()
	var offset int64
	for seq := uint64(1); ; seq++ {
		body, err := reader.Next()
		if err != nil {
			return
		}
		var header coreprotocol.BulkHeader
		data, err := coreprotocol.DecodePrefix(body, &header)
		if err != nil || header.Seq != seq || header.Offset != offset || header.Fault != nil {
			fail(errors.New("invalid upload sequence"))
			return
		}
		if header.Final {
			if len(data) != 0 || offset != in.Size || header.Digest != in.Digest || hex.EncodeToString(hash.Sum(nil)) != in.Digest {
				fail(errors.New("upload digest or length mismatch"))
				return
			}
			// Publication remains a separate durable command under its original
			// request identity. A lost commit must never trigger staging cleanup.
			staged = true
			if send(s, &coreprotocol.BulkHeader{Seq: seq, Offset: offset, Final: true, Digest: in.Digest}) == nil {
				awaitBulkAck(reader, seq)
			}
			_ = s.CloseWrite()
			return
		}
		if len(data) == 0 || len(data) > coreprotocol.BulkChunkBytes || int64(len(data)) > in.Size-offset {
			fail(errors.New("invalid upload length"))
			return
		}
		_, err = m.run(ctx, c.Owner, open.WorkspaceID, store, coreprotocol.ReferenceUploadChunk, coreprotocol.ReferenceUploadCommand{SessionID: in.SessionID, UploadID: id, Offset: offset, Data: data})
		if err != nil {
			fail(err)
			return
		}
		_, _ = hash.Write(data)
		offset += int64(len(data))
	}
}
