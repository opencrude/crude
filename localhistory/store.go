package localhistory

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/restic/chunker"
	_ "modernc.org/sqlite"
)

const (
	chunkMagic         = "CRLHCH01"
	chunkMin           = 256 * 1024
	chunkMax           = 8 * 1024 * 1024
	defaultMaxFileSize = 100 * 1024 * 1024
	chunkPolynomial    = chunker.Pol(0x3DA3358B4DC173)
)

var chunkBufferPool = sync.Pool{New: func() any { buffer := make([]byte, chunkMax); return &buffer }}

type Store struct {
	mu            sync.Mutex
	root          string
	workspaceRoot string
	db            *sql.DB
	processLock   *flock.Flock
	beforeApply   func(index int, change Change) error
}

func Open(storeRoot, workspaceRoot string) (*Store, error) {
	root, err := filepath.Abs(storeRoot)
	if err != nil {
		return nil, fmt.Errorf("localhistory: resolve store root: %w", err)
	}
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("localhistory: resolve workspace root: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(workspace); resolveErr == nil {
		workspace = resolved
	}
	if sameOrInside(root, workspace) {
		return nil, errors.New("localhistory: store must be outside workspace")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("localhistory: create store root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("localhistory: resolve created store root: %w", err)
	}
	if sameOrInside(resolvedRoot, workspace) {
		return nil, errors.New("localhistory: store must be outside workspace")
	}
	root = resolvedRoot
	for _, dir := range []string{
		filepath.Join(root, "objects", "chunks"),
		filepath.Join(root, "objects", "files"),
		filepath.Join(root, "manifests"),
		filepath.Join(root, "plans"),
		filepath.Join(root, "tmp"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("localhistory: create store: %w", err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "history.db"))
	if err != nil {
		return nil, fmt.Errorf("localhistory: open metadata: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrateMetadata(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{root: root, workspaceRoot: workspace, db: db, processLock: flock.New(filepath.Join(root, "mutation.lock"))}
	if err := store.recoverManifestIndex(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.recoverPlanIndex(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.MarkInterruptedScopes(time.Now()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("localhistory: recover interrupted scopes: %w", err)
	}
	return store, nil
}

func (s *Store) Root() string          { return s.root }
func (s *Store) WorkspaceRoot() string { return s.workspaceRoot }

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.processLock != nil {
		_ = s.processLock.Unlock()
	}
	return s.db.Close()
}

func (s *Store) lockMutation(ctx context.Context) (func(), error) {
	if s.processLock == nil {
		return func() {}, nil
	}
	locked, err := s.processLock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, ctx.Err()
	}
	return func() { _ = s.processLock.Unlock() }, nil
}

func sameOrInside(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func objectPath(root, kind, hash, suffix string) (string, error) {
	if len(hash) != sha256.Size*2 {
		return "", errors.New("localhistory: invalid object hash")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", errors.New("localhistory: invalid object hash")
	}
	return filepath.Join(root, "objects", kind, hash[:2], hash+suffix), nil
}

func (s *Store) storeRegularFile(ctx context.Context, path string) (Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Entry{}, err
	}
	if info.Size() <= chunkMin {
		data, err := io.ReadAll(f)
		if err != nil {
			return Entry{}, err
		}
		obj := FileObject{Version: FormatVersion, Hash: sha256Hex(data), Size: int64(len(data))}
		if len(data) > 0 {
			hash := sha256Hex(data)
			if err := s.writeChunk(hash, data); err != nil {
				return Entry{}, err
			}
			obj.Chunks = []ChunkRef{{Hash: hash, Size: int64(len(data))}}
		}
		if err := s.writeFileObject(obj); err != nil {
			return Entry{}, err
		}
		return Entry{Type: EntryFile, Mode: uint32(info.Mode().Perm()), Hash: obj.Hash, Size: obj.Size}, nil
	}
	chnkr := chunker.New(f, chunkPolynomial, chunker.WithBoundaries(chunkMin, chunkMax))
	buffer := chunkBufferPool.Get().(*[]byte)
	buf := *buffer
	defer chunkBufferPool.Put(buffer)
	whole := sha256.New()
	obj := FileObject{Version: FormatVersion}
	for {
		if err := ctx.Err(); err != nil {
			return Entry{}, err
		}
		chunk, err := chnkr.Next(buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Entry{}, fmt.Errorf("chunk %s: %w", path, err)
		}
		data := append([]byte(nil), chunk.Data...)
		if _, err := whole.Write(data); err != nil {
			return Entry{}, err
		}
		hash := sha256Hex(data)
		if err := s.writeChunk(hash, data); err != nil {
			return Entry{}, err
		}
		obj.Chunks = append(obj.Chunks, ChunkRef{Hash: hash, Size: int64(len(data))})
		obj.Size += int64(len(data))
	}
	obj.Hash = hex.EncodeToString(whole.Sum(nil))
	if err := s.writeFileObject(obj); err != nil {
		return Entry{}, err
	}
	return Entry{Type: EntryFile, Mode: uint32(info.Mode().Perm()), Hash: obj.Hash, Size: obj.Size}, nil
}

func (s *Store) writeChunk(hash string, data []byte) error {
	path, err := objectPath(s.root, "chunks", hash, "")
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, 0o600, func(w io.Writer) error {
		if _, err := io.WriteString(w, chunkMagic); err != nil {
			return err
		}
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(data)))
		if _, err := w.Write(size[:]); err != nil {
			return err
		}
		zw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			return err
		}
		if _, err := zw.Write(data); err != nil {
			_ = zw.Close()
			return err
		}
		return zw.Close()
	})
}

func (s *Store) readChunk(hash string) ([]byte, error) {
	path, err := objectPath(s.root, "chunks", hash, "")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	magic := make([]byte, len(chunkMagic))
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != chunkMagic {
		return nil, errors.New("localhistory: corrupt chunk header")
	}
	var size [8]byte
	if _, err := io.ReadFull(f, size[:]); err != nil {
		return nil, errors.New("localhistory: corrupt chunk size")
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("localhistory: corrupt chunk compression: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(zr, int64(binary.BigEndian.Uint64(size[:]))+1))
	closeErr := zr.Close()
	if readErr != nil || closeErr != nil || uint64(len(data)) != binary.BigEndian.Uint64(size[:]) || sha256Hex(data) != hash {
		return nil, errors.New("localhistory: chunk hash mismatch")
	}
	return data, nil
}

func (s *Store) writeFileObject(obj FileObject) error {
	path, err := objectPath(s.root, "files", obj.Hash, ".json")
	if err != nil {
		return err
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		if sha256Hex(existing) == sha256Hex(data) {
			return nil
		}
		return errors.New("localhistory: file object collision")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeBytesAtomic(path, data, 0o600)
}

func (s *Store) readFileObject(hash string) (FileObject, error) {
	path, err := objectPath(s.root, "files", hash, ".json")
	if err != nil {
		return FileObject{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FileObject{}, err
	}
	var obj FileObject
	if err := json.Unmarshal(data, &obj); err != nil || obj.Version != FormatVersion || obj.Hash != hash {
		return FileObject{}, errors.New("localhistory: corrupt file object")
	}
	whole := sha256.New()
	var size int64
	for _, ref := range obj.Chunks {
		chunk, err := s.readChunk(ref.Hash)
		if err != nil {
			return FileObject{}, err
		}
		if int64(len(chunk)) != ref.Size {
			return FileObject{}, errors.New("localhistory: chunk size mismatch")
		}
		_, _ = whole.Write(chunk)
		size += int64(len(chunk))
	}
	if size != obj.Size || hex.EncodeToString(whole.Sum(nil)) != obj.Hash {
		return FileObject{}, errors.New("localhistory: file object hash mismatch")
	}
	return obj, nil
}

func (s *Store) materializeFile(hash, path string) error {
	obj, err := s.readFileObject(hash)
	if err != nil {
		return err
	}
	return writeAtomic(path, 0o600, func(w io.Writer) error {
		for _, ref := range obj.Chunks {
			chunk, err := s.readChunk(ref.Hash)
			if err != nil {
				return err
			}
			if _, err := w.Write(chunk); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) readFileBytes(hash string) ([]byte, error) {
	obj, err := s.readFileObject(hash)
	if err != nil {
		return nil, err
	}
	data := make([]byte, 0, obj.Size)
	for _, ref := range obj.Chunks {
		chunk, err := s.readChunk(ref.Hash)
		if err != nil {
			return nil, err
		}
		data = append(data, chunk...)
	}
	return data, nil
}

func (s *Store) storeBytes(ctx context.Context, data []byte, mode os.FileMode) (Entry, error) {
	tmp, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "merge-*")
	if err != nil {
		return Entry{}, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return Entry{}, err
	}
	if err := tmp.Close(); err != nil {
		return Entry{}, err
	}
	entry, err := s.storeRegularFile(ctx, name)
	entry.Mode = uint32(mode.Perm())
	return entry, err
}

func writeBytesAtomic(path string, data []byte, mode os.FileMode) error {
	return writeAtomic(path, mode, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

func writeImmutable(path string, data []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == string(data) {
			return nil
		}
		return errors.New("localhistory: immutable object collision")
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeBytesAtomic(path, data, mode)
}

func writeAtomic(path string, mode os.FileMode, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".crude-history-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := write(tmp); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		// Windows cannot replace an existing file with Rename. Restores are
		// protected by a safety snapshot, so remove only after the complete temp
		// file has been synced and retry the final rename.
		if runtime.GOOS != "windows" {
			return err
		}
		if _, statErr := os.Stat(path); statErr != nil {
			return err
		}
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		if retryErr := os.Rename(name, path); retryErr != nil {
			return retryErr
		}
	}
	ok = true
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
