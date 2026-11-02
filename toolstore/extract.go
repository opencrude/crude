package toolstore

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ulikunitz/xz"
)

const maxLinkTarget = 4096

// errExtractBudget reports an archive that expands past its byte or entry
// budget, for example a decompression bomb.
var errExtractBudget = errors.New("the archive expands past its budget")

type extractLimits struct {
	bytes   int64
	entries int
}

type extractStats struct {
	bytes int64
	files int
}

type pendingLink struct {
	name, target string
}

// extractor writes archive entries below root. It creates no link until every
// file is written, and it never writes through a link, so no entry can reach
// outside root.
type extractor struct {
	ctx     context.Context
	root    string
	limits  extractLimits
	written int64
	entries int
	files   map[string]bool // regular file -> executable
	dirs    map[string]bool
	links   []pendingLink
}

// extractArchive unpacks archivePath of the given kind into root, which must
// exist and be empty. bin must name a regular file in the result.
func extractArchive(ctx context.Context, kind, archivePath, root, bin string, limits extractLimits) (extractStats, error) {
	binName, err := cleanEntryName(bin)
	if err != nil || binName == "" {
		return extractStats{}, fmt.Errorf("executable path %q is not a contained file path", bin)
	}
	x := &extractor{
		ctx:    ctx,
		root:   root,
		limits: limits,
		files:  make(map[string]bool),
		dirs:   map[string]bool{"": true, ".": true},
	}
	switch kind {
	case ArchiveZip:
		err = x.zip(archivePath)
	case ArchiveTarGz, ArchiveTarXz, ArchiveGz, ArchiveRaw:
		err = x.stream(kind, archivePath, binName)
	default:
		err = fmt.Errorf("archive kind %q is not supported", kind)
	}
	if err != nil {
		return extractStats{}, err
	}
	if err := x.createLinks(); err != nil {
		return extractStats{}, err
	}
	binPath := x.abs(binName)
	info, err := os.Lstat(binPath)
	if err != nil || !info.Mode().IsRegular() {
		return extractStats{}, fmt.Errorf("the archive has no regular file at %q", binName)
	}
	if err := os.Chmod(binPath, info.Mode().Perm()|0o700); err != nil {
		return extractStats{}, err
	}
	return extractStats{bytes: x.written, files: len(x.files)}, nil
}

func (x *extractor) stream(kind, archivePath, binName string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	buffered := bufio.NewReader(file)
	switch kind {
	case ArchiveTarGz:
		reader, err := gzip.NewReader(buffered)
		if err != nil {
			return fmt.Errorf("the archive is not gzip data: %w", err)
		}
		defer func() { _ = reader.Close() }()
		return x.tar(reader)
	case ArchiveTarXz:
		reader, err := xz.NewReader(buffered)
		if err != nil {
			return fmt.Errorf("the archive is not xz data: %w", err)
		}
		return x.tar(reader)
	case ArchiveGz:
		if strings.Contains(binName, "/") {
			return fmt.Errorf("executable %q must be a file name for a single compressed file", binName)
		}
		reader, err := gzip.NewReader(buffered)
		if err != nil {
			return fmt.Errorf("the download is not gzip data: %w", err)
		}
		defer func() { _ = reader.Close() }()
		return x.addFile(binName, reader, true)
	default:
		if strings.Contains(binName, "/") {
			return fmt.Errorf("executable %q must be a file name for a single file download", binName)
		}
		return x.addFile(binName, buffered, true)
	}
}

func (x *extractor) zip(archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("the archive is not a valid zip file: %w", err)
	}
	defer func() { _ = reader.Close() }()
	if len(reader.File) > x.limits.entries {
		return fmt.Errorf("%w: it has %d entries and the budget is %d", errExtractBudget, len(reader.File), x.limits.entries)
	}
	for _, file := range reader.File {
		if err := x.ctx.Err(); err != nil {
			return err
		}
		name, err := cleanEntryName(file.Name)
		if err != nil {
			return err
		}
		mode := file.Mode()
		if name == "" {
			if mode.IsDir() {
				continue
			}
			return errors.New("the archive has an entry without a name")
		}
		switch {
		case mode&fs.ModeSymlink != 0:
			target, err := readZipLink(file)
			if err != nil {
				return err
			}
			if err := x.addLink(name, target); err != nil {
				return err
			}
		case mode.IsDir():
			if err := x.addDir(name); err != nil {
				return err
			}
		case mode.IsRegular():
			if err := x.addZipFile(name, file); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q is a device, pipe or other special file", name)
		}
	}
	return nil
}

func readZipLink(file *zip.File) (string, error) {
	reader, err := file.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, maxLinkTarget+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxLinkTarget {
		return "", fmt.Errorf("archive link %q has a target that is too long", file.Name)
	}
	return string(data), nil
}

func (x *extractor) addZipFile(name string, file *zip.File) error {
	if file.UncompressedSize64 > uint64(x.limits.bytes-x.written) {
		return fmt.Errorf("%w of %d bytes", errExtractBudget, x.limits.bytes)
	}
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	return x.addFile(name, reader, file.Mode()&0o111 != 0)
}

func (x *extractor) tar(reader io.Reader) error {
	archive := tar.NewReader(reader)
	for {
		if err := x.ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("the archive is damaged: %w", err)
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name, err := cleanEntryName(header.Name)
		if err != nil {
			return err
		}
		if name == "" {
			if header.Typeflag == tar.TypeDir {
				continue
			}
			return errors.New("the archive has an entry without a name")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = x.addDir(name)
		case tar.TypeReg:
			if header.Size > x.limits.bytes-x.written {
				return fmt.Errorf("%w of %d bytes", errExtractBudget, x.limits.bytes)
			}
			err = x.addFile(name, archive, header.Mode&0o111 != 0)
		case tar.TypeSymlink:
			err = x.addLink(name, header.Linkname)
		case tar.TypeLink:
			err = x.addHardLink(name, header.Linkname)
		default:
			err = fmt.Errorf("archive entry %q is a device, pipe or other special file", name)
		}
		if err != nil {
			return err
		}
	}
}

func (x *extractor) countEntry() error {
	x.entries++
	if x.entries > x.limits.entries {
		return fmt.Errorf("%w of %d entries", errExtractBudget, x.limits.entries)
	}
	return nil
}

func (x *extractor) abs(name string) string {
	return filepath.Join(x.root, filepath.FromSlash(name))
}

func (x *extractor) addDir(name string) error {
	if err := x.countEntry(); err != nil {
		return err
	}
	if _, isFile := x.files[name]; isFile {
		return fmt.Errorf("archive path %q is both a file and a folder", name)
	}
	return x.ensureDir(name)
}

// ensureDir creates the folder name and its parents as real folders. It
// refuses a path that passes through a file or a link.
func (x *extractor) ensureDir(name string) error {
	if x.dirs[name] {
		return nil
	}
	current := ""
	for _, part := range strings.Split(name, "/") {
		current = path.Join(current, part)
		if x.dirs[current] {
			continue
		}
		target := x.abs(current)
		info, err := os.Lstat(target)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(target, 0o700); err != nil {
				return err
			}
		case err != nil:
			return err
		case !info.IsDir():
			return fmt.Errorf("archive path %q passes through a file or a link", name)
		}
		x.dirs[current] = true
	}
	return nil
}

func (x *extractor) addFile(name string, reader io.Reader, executable bool) error {
	if err := x.countEntry(); err != nil {
		return err
	}
	return x.writeFile(name, reader, executable)
}

func (x *extractor) writeFile(name string, reader io.Reader, executable bool) error {
	if x.dirs[name] {
		return fmt.Errorf("archive path %q is both a file and a folder", name)
	}
	if err := x.ensureDir(path.Dir(name)); err != nil {
		return err
	}
	perm := os.FileMode(0o600)
	if executable {
		perm = 0o700
	}
	target := x.abs(name)
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("the archive contains %q more than once", name)
	}
	if err != nil {
		return err
	}
	remaining := x.limits.bytes - x.written
	written, copyErr := io.Copy(file, io.LimitReader(&contextReader{ctx: x.ctx, reader: reader}, remaining+1))
	x.written += written
	syncErr := file.Sync()
	closeErr := file.Close()
	switch {
	case copyErr != nil:
		return fmt.Errorf("extract %q: %w", name, copyErr)
	case x.written > x.limits.bytes:
		return fmt.Errorf("%w of %d bytes", errExtractBudget, x.limits.bytes)
	case syncErr != nil:
		return syncErr
	case closeErr != nil:
		return closeErr
	}
	if err := os.Chmod(target, perm); err != nil {
		return err
	}
	x.files[name] = executable
	return nil
}

func (x *extractor) addLink(name, target string) error {
	if err := x.countEntry(); err != nil {
		return err
	}
	if target == "" || len(target) > maxLinkTarget || strings.HasPrefix(target, "/") {
		return fmt.Errorf("archive link %q has an absolute or empty target", name)
	}
	for _, r := range target {
		if r == '\\' || r == ':' || r < 0x20 || r == 0x7f {
			return fmt.Errorf("archive link %q has a target with a character that is not permitted", name)
		}
	}
	resolved := path.Join(path.Dir(name), target)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("archive link %q points outside the tool folder", name)
	}
	x.links = append(x.links, pendingLink{name: name, target: target})
	return nil
}

// addHardLink copies an earlier regular file. The copy counts against the
// byte budget.
func (x *extractor) addHardLink(name, target string) error {
	if err := x.countEntry(); err != nil {
		return err
	}
	source, err := cleanEntryName(target)
	if err != nil {
		return err
	}
	executable, ok := x.files[source]
	if !ok {
		return fmt.Errorf("archive hard link %q does not point to an earlier file in the archive", name)
	}
	file, err := os.Open(x.abs(source))
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return x.writeFile(name, file, executable)
}

// createLinks creates the symbolic links after all files exist, then checks
// that each link resolves to an existing path inside root.
func (x *extractor) createLinks() error {
	if len(x.links) == 0 {
		return nil
	}
	for _, link := range x.links {
		if err := x.ensureDir(path.Dir(link.name)); err != nil {
			return err
		}
		if x.dirs[link.name] {
			return fmt.Errorf("archive path %q is both a link and a folder", link.name)
		}
		if err := os.Symlink(filepath.FromSlash(link.target), x.abs(link.name)); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("the archive contains %q more than once", link.name)
			}
			return fmt.Errorf("this system cannot create the archive link %q: %w", link.name, err)
		}
	}
	realRoot, err := filepath.EvalSymlinks(x.root)
	if err != nil {
		return err
	}
	for _, link := range x.links {
		resolved, err := filepath.EvalSymlinks(x.abs(link.name))
		if err != nil {
			return fmt.Errorf("archive link %q points to a missing file or a link loop", link.name)
		}
		rel, err := filepath.Rel(realRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("archive link %q points outside the tool folder", link.name)
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
