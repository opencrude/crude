package toolstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"crude/fileio"
)

// A preparation marker is written before the callback may start a process.
// Invalid markers fail closed: an ambiguous interrupted build must not replay.
func (s *Store) refusePendingPreparation(ctx context.Context, pkg, version string) error {
	if err := s.mu.LockContext(ctx); err != nil {
		return err
	}
	pending := s.preparationUnconfirmed[installKey(pkg, version)]
	_, records, uncertain, err := preparationReservations(ctx, s.root)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if uncertain {
		return &PreparationUnverifiedError{Err: errors.New("the tool preparation records need inspection")}
	}
	for _, record := range records {
		if record == preparationPendingDir+"/"+pkg+"/"+version {
			pending = true
		}
	}
	if pending {
		return &PreparationUnverifiedError{Err: errors.New("a previous preparation needs inspection")}
	}
	entries, err := os.ReadDir(filepath.Join(s.root, stagingName))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "install-") {
			continue
		}
		name := filepath.Join(s.root, stagingName, entry.Name(), preparationPendingName)
		data, err := fileio.ReadRegularNoFollow(ctx, name, maxManifestBytes)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &PreparationUnverifiedError{Err: fmt.Errorf("inspect pending tool preparation: %w", err)}
		}
		var marker manifest
		if err := json.Unmarshal(data, &marker); err != nil {
			return &PreparationUnverifiedError{Err: errors.New("a tool preparation marker needs inspection")}
		}
		canonical, err := json.MarshalIndent(marker, "", "  ")
		if err != nil || !bytes.Equal(data, append(canonical, '\n')) || marker.Archive != ArchivePrepared ||
			validComponent(marker.Package) != nil || validComponent(marker.Version) != nil {
			return &PreparationUnverifiedError{Err: errors.New("a tool preparation marker needs inspection")}
		}
		if marker.Package == pkg && marker.Version == version {
			return &PreparationUnverifiedError{Err: errors.New("a previous preparation needs inspection")}
		}
	}
	return ctx.Err()
}

func (s *Store) prepareTree(ctx context.Context, r Request, staging string, stageIdentity os.FileInfo, content string, retain *bool) (string, extractStats, error) {
	if err := s.checkURL(r.URL); err != nil {
		return "", extractStats{}, err
	}
	output, scratch := filepath.Join(staging, "output"), filepath.Join(staging, "scratch")
	for _, dir := range []string{output, scratch} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", extractStats{}, err
		}
	}
	outputRoot, err := os.OpenRoot(output)
	if err != nil {
		return "", extractStats{}, err
	}
	defer func() { _ = outputRoot.Close() }()
	outputIdentity, err := outputRoot.Stat(".")
	if err != nil {
		return "", extractStats{}, err
	}
	marker := filepath.Join(staging, preparationPendingName)
	temporary := marker + ".new"
	identity := manifest{Package: r.Package, Version: r.Version, Source: r.Source, URL: r.URL,
		Archive: r.Archive, Executable: r.Bin, Command: r.Command, InstalledBy: r.InstalledBy, Approval: r.Approval}
	if err := writeManifest(temporary, identity); err != nil {
		return "", extractStats{}, err
	}
	if err := os.Rename(temporary, marker); err != nil {
		return "", extractStats{}, err
	}
	markerIdentity, err := os.Lstat(marker)
	if err != nil || !markerIdentity.Mode().IsRegular() {
		return "", extractStats{}, errors.New("the tool preparation marker could not be checked")
	}
	markerBytes, err := fileio.ReadRegularNoFollow(ctx, marker, maxManifestBytes)
	if err != nil {
		return "", extractStats{}, err
	}
	if err := syncDir(staging); err != nil {
		return "", extractStats{}, err
	}
	if err := syncDir(filepath.Dir(staging)); err != nil {
		return "", extractStats{}, err
	}
	*retain = true
	reservation, err := s.beginPreparation(ctx, r, staging)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			*retain = false
			return "", extractStats{}, err
		}
		return "", extractStats{}, &PreparationUnverifiedError{Err: err}
	}
	finish := func() error {
		if !sameStagingFolder(staging, stageIdentity) {
			return &PreparationUnverifiedError{Err: errors.New("the staging folder changed")}
		}
		current, err := os.Lstat(marker)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(markerIdentity, current) {
			return &PreparationUnverifiedError{Err: errors.New("the tool preparation marker changed")}
		}
		currentBytes, err := fileio.ReadRegularNoFollow(context.Background(), marker, maxManifestBytes)
		if err != nil || !bytes.Equal(markerBytes, currentBytes) {
			return &PreparationUnverifiedError{Err: errors.New("the tool preparation marker changed")}
		}
		if err := s.finishPreparation(reservation); err != nil {
			return &PreparationUnverifiedError{Err: err}
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		if finishErr := finish(); finishErr != nil {
			return "", extractStats{}, errors.Join(err, finishErr)
		}
		*retain = false
		return "", extractStats{}, err
	}
	err = r.Prepare(ctx, output, scratch)
	if !sameStagingFolder(staging, stageIdentity) {
		return "", extractStats{}, &PreparationUnverifiedError{Err: errors.New("the staging folder changed")}
	}
	var uncertain *PreparationUnverifiedError
	var processUncertain *fileio.CommandTreeUnverifiedError
	if errors.As(err, &uncertain) {
		return "", extractStats{}, err
	}
	if errors.As(err, &processUncertain) {
		return "", extractStats{}, &PreparationUnverifiedError{Err: err}
	}
	// Callback return without an uncertain error positively verifies that its
	// owned processes were joined. Failed work can now be removed.
	if err != nil {
		if finishErr := finish(); finishErr != nil {
			return "", extractStats{}, errors.Join(err, finishErr)
		}
		*retain = false
		return "", extractStats{}, err
	}
	if err := ctx.Err(); err != nil {
		if finishErr := finish(); finishErr != nil {
			return "", extractStats{}, errors.Join(err, finishErr)
		}
		*retain = false
		return "", extractStats{}, err
	}
	current, err := os.Lstat(output)
	if err != nil || !current.IsDir() || !os.SameFile(outputIdentity, current) {
		if finishErr := finish(); finishErr != nil {
			return "", extractStats{}, finishErr
		}
		*retain = false
		return "", extractStats{}, errors.New("the preparation output folder changed")
	}
	if err := finish(); err != nil {
		return "", extractStats{}, err
	}
	if err := os.Remove(marker); err != nil {
		return "", extractStats{}, err
	}
	if err := syncDir(staging); err != nil {
		return "", extractStats{}, err
	}
	*retain = false
	stats, err := copyPreparedTree(ctx, outputRoot, content, r.Bin, extractLimits{bytes: r.MaxExtractedBytes, entries: r.MaxEntries})
	if err != nil {
		return "", extractStats{}, err
	}
	digest, err := preparedTreeDigest(ctx, content, extractLimits{bytes: r.MaxExtractedBytes, entries: r.MaxEntries})
	return digest, stats, err
}

// copyPreparedTree copies files rather than adopting the process's tree. It
// rejects special files and escaping links, and bounds the real output bytes.
// The retained root confines traversal; the reader checks each file identity.
func copyPreparedTree(ctx context.Context, root *os.Root, dest, bin string, limits extractLimits) (extractStats, error) {
	x := &extractor{ctx: ctx, root: dest, limits: limits, files: make(map[string]bool), dirs: map[string]bool{"": true, ".": true}}
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		clean, err := cleanEntryName(name)
		if err != nil || clean != name {
			return fmt.Errorf("prepared path %q is not safe", name)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return x.addDir(name)
		case info.Mode().IsRegular():
			file, err := fileio.OpenRootRegular(ctx, root, filepath.FromSlash(name))
			if err != nil {
				return err
			}
			opened, err := file.Stat()
			if err == nil {
				err = x.addFile(name, file, opened.Mode()&0o111 != 0)
			}
			return errors.Join(err, file.Close())
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := root.Readlink(filepath.FromSlash(name))
			if err != nil {
				return err
			}
			return x.addLink(name, filepath.ToSlash(target))
		default:
			return fmt.Errorf("prepared path %q is a device, pipe or other special file", name)
		}
	})
	if err != nil {
		return extractStats{}, err
	}
	if err := x.createLinks(); err != nil {
		return extractStats{}, err
	}
	info, err := os.Lstat(x.abs(bin))
	if err != nil || !info.Mode().IsRegular() {
		return extractStats{}, fmt.Errorf("the prepared tree has no regular file at %q", bin)
	}
	if err := os.Chmod(x.abs(bin), info.Mode().Perm()|0o700); err != nil {
		return extractStats{}, err
	}
	return extractStats{bytes: x.written, files: len(x.files)}, nil
}

// preparedTreeDigest binds paths, executable bits, link targets and contents.
// Modification times and writable bits are excluded because sealing changes them.
func preparedTreeDigest(ctx context.Context, dir string, limits extractLimits) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	digest := sha256.New()
	_, _ = io.WriteString(digest, "crude-prepared-tree-v1\n")
	var total int64
	entries := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		entries++
		if entries > limits.entries {
			return fmt.Errorf("%w of %d entries", errExtractBudget, limits.entries)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			_, _ = fmt.Fprintf(digest, "dir %q\n", name)
		case info.Mode().IsRegular():
			file, err := fileio.OpenRootRegular(ctx, root, filepath.FromSlash(name))
			if err != nil {
				return err
			}
			opened, err := file.Stat()
			if err != nil {
				_ = file.Close()
				return err
			}
			content := sha256.New()
			size, copyErr := io.Copy(content, io.LimitReader(&contextReader{ctx: ctx, reader: file}, limits.bytes-total+1))
			after, statErr := file.Stat()
			closeErr := file.Close()
			if err := errors.Join(copyErr, statErr, closeErr); err != nil {
				return err
			}
			total += size
			if total > limits.bytes {
				return fmt.Errorf("%w of %d bytes", errExtractBudget, limits.bytes)
			}
			if size != opened.Size() || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
				return errors.New("the prepared file changed while being checked")
			}
			_, _ = fmt.Fprintf(digest, "file %q %t %d %x\n", name, opened.Mode()&0o111 != 0, size, content.Sum(nil))
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := root.Readlink(filepath.FromSlash(name))
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(digest, "link %q %q\n", name, filepath.ToSlash(target))
		default:
			return fmt.Errorf("prepared path %q is not a file, folder or link", name)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
