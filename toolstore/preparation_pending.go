package toolstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"crude/fileio"
)

const preparationPendingDir = ".preparation-pending"

type preparationRecord struct {
	Package string `json:"package"`
	Version string `json:"version"`
	Stage   string `json:"stage"`
}

type preparationReservation struct {
	path                string
	record              preparationRecord
	data                []byte
	identity            os.FileInfo
	rootDir, packageDir os.FileInfo
}

func sameStagingFolder(path string, identity os.FileInfo) bool {
	current, err := os.Lstat(path)
	return err == nil && current.IsDir() && identity != nil && os.SameFile(identity, current)
}

func preparationRecordBytes(record preparationRecord) ([]byte, error) {
	data, err := json.Marshal(record)
	return append(data, '\n'), err
}

func preparationRecordPath(root, pkg, version string) string {
	return filepath.Join(root, preparationPendingDir, pkg, version)
}

// beginPreparation serializes with store scans and writes a durable reservation
// outside the process-owned staging tree. A moved stage cannot erase the fence.
func (s *Store) beginPreparation(ctx context.Context, r Request, staging string) (*preparationReservation, error) {
	if err := s.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record := preparationRecord{Package: r.Package, Version: r.Version, Stage: filepath.Base(staging)}
	data, err := preparationRecordBytes(record)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.root, preparationPendingDir)
	for _, part := range []string{dir, filepath.Join(dir, r.Package)} {
		if err := os.Mkdir(part, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, err := os.Lstat(part)
		if err != nil || !info.IsDir() {
			return nil, errors.New("the tool preparation record is not a directory")
		}
		if err := syncDir(filepath.Dir(part)); err != nil {
			return nil, err
		}
	}
	rootIdentity, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	packageIdentity, err := os.Lstat(filepath.Join(dir, r.Package))
	if err != nil {
		return nil, err
	}
	name := preparationRecordPath(s.root, r.Package, r.Version)
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the tool preparation needs inspection: %w", err)
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	var info os.FileInfo
	if err == nil {
		info, err = file.Stat()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	if err := syncDir(filepath.Dir(name)); err != nil {
		return nil, err
	}
	return &preparationReservation{path: name, record: record, data: data, identity: info,
		rootDir: rootIdentity, packageDir: packageIdentity}, nil
}

// finishPreparation never overwrites a replacement record. When removal cannot
// be confirmed it restores a durable refusal when possible and fences this
// process even if restoration fails.
func (s *Store) finishPreparation(reservation *preparationReservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reservation == nil {
		return errors.New("the tool preparation record is missing")
	}
	key := installKey(reservation.record.Package, reservation.record.Version)
	if !sameStagingFolder(filepath.Join(s.root, preparationPendingDir), reservation.rootDir) ||
		!sameStagingFolder(filepath.Dir(reservation.path), reservation.packageDir) {
		s.preparationUnconfirmed[key] = true
		return errors.New("the tool preparation record folder changed")
	}
	fail := func(cause error) error {
		s.preparationUnconfirmed[key] = true
		if _, err := os.Lstat(reservation.path); errors.Is(err, fs.ErrNotExist) {
			file, err := os.OpenFile(reservation.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err == nil {
				_, writeErr := file.Write(reservation.data)
				syncErr := file.Sync()
				closeErr := file.Close()
				err = errors.Join(writeErr, syncErr, closeErr, syncDir(filepath.Dir(reservation.path)))
			}
			cause = errors.Join(cause, err)
		}
		return cause
	}
	info, err := os.Lstat(reservation.path)
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(reservation.identity, info) {
		return fail(errors.New("the tool preparation record changed"))
	}
	data, err := fileio.ReadRegularNoFollow(context.Background(), reservation.path, maxManifestBytes)
	if err != nil || !bytes.Equal(data, reservation.data) {
		return fail(errors.New("the tool preparation record changed"))
	}
	if err := os.Remove(reservation.path); err != nil {
		return fail(err)
	}
	if err := syncDir(filepath.Dir(reservation.path)); err != nil {
		return fail(err)
	}
	delete(s.preparationUnconfirmed, key)
	return nil
}

// preparationReservations reads canonical records without treating malformed
// bytes or unknown entries as permission to prune staging.
func preparationReservations(ctx context.Context, root string) (map[string]bool, []string, bool, error) {
	stages := make(map[string]bool)
	dir := filepath.Join(root, preparationPendingDir)
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return stages, nil, false, nil
	}
	if err != nil || !info.IsDir() {
		return stages, []string{preparationPendingDir}, true, ctx.Err()
	}
	packages, err := os.ReadDir(dir)
	if err != nil {
		return stages, []string{preparationPendingDir}, true, ctx.Err()
	}
	var unknown []string
	uncertain := false
	for _, pkg := range packages {
		if err := ctx.Err(); err != nil {
			return nil, nil, false, err
		}
		if !pkg.IsDir() || validComponent(pkg.Name()) != nil {
			unknown = append(unknown, preparationPendingDir+"/"+pkg.Name())
			uncertain = true
			continue
		}
		versions, err := os.ReadDir(filepath.Join(dir, pkg.Name()))
		if err != nil {
			unknown = append(unknown, preparationPendingDir+"/"+pkg.Name())
			uncertain = true
			continue
		}
		for _, version := range versions {
			if err := ctx.Err(); err != nil {
				return nil, nil, false, err
			}
			relative := preparationPendingDir + "/" + pkg.Name() + "/" + version.Name()
			unknown = append(unknown, relative)
			if !version.Type().IsRegular() || validComponent(version.Name()) != nil {
				uncertain = true
				continue
			}
			path := preparationRecordPath(root, pkg.Name(), version.Name())
			data, err := fileio.ReadRegularNoFollow(ctx, path, maxManifestBytes)
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, false, ctx.Err()
				}
				uncertain = true
				continue
			}
			var record preparationRecord
			if err := json.Unmarshal(data, &record); err != nil {
				uncertain = true
				continue
			}
			canonical, err := preparationRecordBytes(record)
			if err != nil || !bytes.Equal(data, canonical) || record.Package != pkg.Name() ||
				record.Version != version.Name() || !strings.HasPrefix(record.Stage, "install-") ||
				validComponent(record.Stage) != nil {
				uncertain = true
				continue
			}
			stages[record.Stage] = true
		}
	}
	return stages, unknown, uncertain, ctx.Err()
}
