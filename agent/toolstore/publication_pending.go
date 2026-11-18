package toolstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// A publication marker is durable before a tool is moved into the store. It
// remains until the tool and its install history have both been confirmed.
// A missing or unreadable result is never silently repaired or deleted.
const publicationPendingDir = ".publication-pending"

type publicationReservation struct {
	path     string
	name     string
	identity os.FileInfo
}

func publicationMarker(packageDir, dir string) string {
	return filepath.Join(filepath.Dir(packageDir), publicationPendingDir, filepath.Base(packageDir), dir)
}

func ensurePublicationPendingDir(packageDir string) (string, error) {
	root := filepath.Dir(packageDir)
	dir := filepath.Join(root, publicationPendingDir)
	for _, next := range []string{dir, filepath.Join(dir, filepath.Base(packageDir))} {
		if err := os.Mkdir(next, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		info, err := os.Lstat(next)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("the tool publication record is not a directory: %s", next)
		}
		if err := syncDir(filepath.Dir(next)); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, filepath.Base(packageDir)), nil
}

// beginPublication and finishPublication run while the store mutex is held.
// The marker's presence blocks lookup, automatic use and a repeated install.
func beginPublication(packageDir, name string) (*publicationReservation, error) {
	dir, err := ensurePublicationPendingDir(packageDir)
	if err != nil {
		return nil, err
	}
	marker := filepath.Join(dir, name)
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the tool publication needs inspection: %w", err)
	}
	if _, err = file.WriteString(name + "\n"); err == nil {
		err = file.Sync()
	}
	var identity os.FileInfo
	if err == nil {
		identity, err = file.Stat()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	if err := syncDir(dir); err != nil {
		return nil, err
	}
	return &publicationReservation{path: marker, name: name, identity: identity}, nil
}

func finishPublication(packageDir string, reservation *publicationReservation) error {
	if reservation == nil || reservation.identity == nil ||
		reservation.path != publicationMarker(packageDir, reservation.name) {
		return errors.New("the tool publication record changed; inspect the store")
	}
	info, err := os.Lstat(reservation.path)
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(reservation.identity, info) {
		return errors.New("the tool publication record changed; inspect the store")
	}
	data, err := readBounded(reservation.path, 1024)
	if err != nil || string(data) != reservation.name+"\n" {
		return errors.New("the tool publication record changed; inspect the store")
	}
	if err := os.Remove(reservation.path); err != nil {
		return err
	}
	dir := filepath.Dir(reservation.path)
	if err := syncDir(dir); err != nil {
		// Restore the refusal if possible. Even if the restoration fails, the
		// caller retains its in-memory refusal and reports the storage error.
		_, restoreErr := beginPublication(packageDir, reservation.name)
		return errors.Join(err, restoreErr)
	}
	return nil
}

// Preserve a refusal if the original record disappeared before completion.
// Never overwrite a replacement record made by another writer.
func preservePublication(packageDir, name string) error {
	pending, err := publicationPending(packageDir, name)
	if err != nil || pending {
		return err
	}
	_, err = beginPublication(packageDir, name)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	return err
}

func publicationPending(packageDir, name string) (bool, error) {
	dir := filepath.Dir(publicationMarker(packageDir, name))
	for _, part := range []string{filepath.Dir(dir), dir} {
		info, err := os.Lstat(part)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() {
			return false, fmt.Errorf("the tool publication record is not a directory: %s", part)
		}
	}
	_, err := os.Lstat(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// A previous attempt can have computed a digest but not confirmed its rename.
// Refuse another download for that exact package/version until it is inspected.
func pendingVersion(ctx context.Context, packageDir, version string) (string, error) {
	dir := filepath.Dir(publicationMarker(packageDir, version))
	for _, part := range []string{filepath.Dir(dir), dir} {
		info, err := os.Lstat(part)
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("the tool publication record is unavailable: %s", part)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	prefix := version + "-"
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && len(name) == len(prefix)+12 {
			digest := strings.TrimPrefix(name, prefix)
			if validDigest(digest + strings.Repeat("0", 52)) {
				return name, nil
			}
		}
	}
	return "", nil
}

// pendingPublications includes records whose target was never created. They
// remain visible for inspection even when no tool directory exists.
func pendingPublications(ctx context.Context, root string) ([]string, error) {
	dir := filepath.Join(root, publicationPendingDir)
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("the tool publication record is unavailable: %s", dir)
	}
	packages, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, pkg := range packages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !pkg.IsDir() || validComponent(pkg.Name()) != nil {
			out = append(out, publicationPendingDir+"/"+pkg.Name())
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, pkg.Name()))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out = append(out, pkg.Name()+"/"+entry.Name())
		}
	}
	return out, nil
}
