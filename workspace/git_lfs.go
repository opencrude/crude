package workspace

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"crude/fileio"
)

// LFS delegates storage, filters, hooks and transfers to installed Git LFS.
// It never migrates existing history or installs application-wide Git settings.
func (s *GitService) LFS(ctx context.Context, operation string, paths ...string) error {
	if s == nil {
		return errors.New("git is unavailable")
	}
	var args []string
	switch operation {
	case "install":
		if len(paths) != 0 {
			return errors.New("git LFS setup does not accept paths")
		}
		args = []string{"lfs", "install", "--local"}
	case "pull":
		if len(paths) != 0 {
			return errors.New("git LFS download does not accept paths")
		}
		args = []string{"lfs", "pull"}
	case "track":
		if len(paths) == 0 {
			return errors.New("select a file to track with Git LFS")
		}
		args = []string{"lfs", "track", "--filename", "--"}
	default:
		return errors.New("unknown Git LFS action")
	}
	repo, root, err := s.open()
	if err != nil {
		return err
	}
	closeGitRepository(repo)
	if _, err := gitStdout(ctx, root, nil, "lfs", "version"); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("git LFS is unavailable. Install git-lfs on the Git host, then try again: %w", err)
	}
	if operation == "pull" {
		_, err := s.runRemote(ctx, args...)
		return err
	}
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if operation == "track" {
		if info, err := os.Lstat(filepath.Join(root, ".gitattributes")); err != nil && !os.IsNotExist(err) {
			return err
		} else if err == nil && !info.Mode().IsRegular() {
			return errors.New(".gitattributes is not a regular file; Git LFS tracking was not changed")
		}
		for _, path := range paths {
			rel, err := gitReviewPath(root, path, "")
			if err != nil {
				return err
			}
			args = append(args, rel)
		}
	}
	return s.run(ctx, args...)
}

// gitLFSPointerSize recognizes the canonical v1 pointer that Git LFS writes.
// It is metadata, never an instruction to fetch content. Attribute lookup still
// covers extended/noncanonical pointers. Bound the check to pointer-sized blobs.
func gitLFSPointerSize(text string) (int64, bool) {
	if len(text) > 1024 {
		return 0, false
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 3 || lines[0] != "version https://git-lfs.github.com/spec/v1" ||
		!strings.HasPrefix(lines[1], "oid sha256:") || !strings.HasPrefix(lines[2], "size ") {
		return 0, false
	}
	hash, err := hex.DecodeString(strings.TrimPrefix(lines[1], "oid sha256:"))
	if err != nil || len(hash) != 32 {
		return 0, false
	}
	size, err := strconv.ParseInt(strings.TrimPrefix(lines[2], "size "), 10, 64)
	return size, err == nil && size >= 0
}

// The Git attribute owner resolves nested attributes, quoting, global attributes
// and overrides. One NUL-delimited batch avoids a subprocess per changed file.
func gitLFSPaths(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	result := make(map[string]bool, len(paths))
	if len(paths) == 0 {
		return result, nil
	}
	var input strings.Builder
	for _, path := range paths {
		input.WriteString(path)
		input.WriteByte(0)
	}
	out, err := gitStdout(ctx, root, []byte(input.String()), "check-attr", "-z", "--stdin", "filter")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+2 < len(fields); i += 3 {
		result[fields[i]] = fields[i+2] == "lfs"
	}
	return result, nil
}
