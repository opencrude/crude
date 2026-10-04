package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	MaxFrontmatterBytes = 16 << 10
	MaxInstructionBytes = 64 << 10
	MaxFileBytes        = 8 << 20
	MaxSkillBytes       = 32 << 20
	MaxFiles            = 512
	MaxEntries          = 1024
	MaxPathDepth        = 32
	MaxDefinitionBytes  = MaxFrontmatterBytes + MaxInstructionBytes + 16
)

var (
	ErrInvalid     = errors.New("invalid skill")
	ErrUnsafePath  = errors.New("unsafe skill path")
	ErrLimit       = errors.New("skill budget exceeded")
	ErrChanged     = errors.New("skill changed since loading")
	ErrUnavailable = errors.New("skill requirements unavailable")
	ErrUnsupported = errors.New("unsupported skill behavior")
)

type Skill struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Path          string
	Digest        string
	Instructions  string
	Metadata      map[string]string
	AllowedTools  []string
	Files         []string

	snapshotPath   string
	snapshotDigest string
	directory      os.FileInfo
	records        map[string]fileRecord
}

type fileRecord struct {
	kind       string
	target     string
	executable fs.FileMode
	size       int64
	content    [sha256.Size]byte
}

func Inspect(path string) (Skill, error) {
	root, absolute, err := openSkillRoot(path)
	if err != nil {
		return Skill{}, err
	}
	defer root.Close()
	frontmatter, err := readFrontmatter(context.Background(), root)
	if err != nil {
		return Skill{}, err
	}
	skill, err := parseFrontmatter(frontmatter, filepath.Base(absolute))
	if err != nil {
		return Skill{}, err
	}
	skill.Path = absolute
	return skill, nil
}

func Load(path string) (Skill, error) {
	return LoadContext(context.Background(), path)
}

func LoadContext(ctx context.Context, path string) (Skill, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	root, absolute, err := openSkillRoot(path)
	if err != nil {
		return Skill{}, err
	}
	defer root.Close()
	return loadRoot(ctx, root, absolute, filepath.Base(absolute), true)
}

func loadRoot(ctx context.Context, root *os.Root, path, name string, availability bool) (Skill, error) {
	definition, definitionRecord, err := readBytes(ctx, root, "SKILL.md", MaxDefinitionBytes)
	if err != nil {
		return Skill{}, fmt.Errorf("load SKILL.md: %w", err)
	}
	frontmatter, instructions, err := splitDefinition(definition)
	if err != nil {
		return Skill{}, err
	}
	skill, err := parseFrontmatter(frontmatter, name)
	if err != nil {
		return Skill{}, err
	}
	skill.Path = path
	skill.Instructions = string(instructions)
	skill.records = make(map[string]fileRecord)
	entries, total := 0, int64(0)
	if err := collectFiles(ctx, root, ".", &skill, &entries, &total); err != nil {
		return Skill{}, err
	}
	if skill.records["SKILL.md"] != definitionRecord {
		return Skill{}, fmt.Errorf("%w: SKILL.md changed during loading", ErrChanged)
	}
	slices.Sort(skill.Files)
	skill.Digest = manifestDigest(skill.records)
	skill.directory, err = root.Stat(".")
	if err != nil {
		return Skill{}, err
	}
	skill.snapshotPath = path
	skill.snapshotDigest = skill.Digest
	if availability {
		return skill, CheckAvailability(skill)
	}
	return skill, nil
}

func ReadFile(skill Skill, relative string, maxBytes int64) ([]byte, error) {
	if err := validateRelative(relative); err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("%w: maxBytes must be positive", ErrLimit)
	}
	if maxBytes > MaxFileBytes {
		maxBytes = MaxFileBytes
	}
	root, absolute, err := openSkillRoot(skill.Path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if skill.Digest == "" {
		return nil, fmt.Errorf("%w: load the skill before reading resources", ErrInvalid)
	}
	if skill.records == nil {
		loaded, err := loadRoot(context.Background(), root, absolute, filepath.Base(absolute), false)
		if err != nil {
			return nil, err
		}
		if loaded.Digest != skill.Digest {
			return nil, ErrChanged
		}
		skill = loaded
	} else {
		info, err := root.Stat(".")
		if err != nil {
			return nil, err
		}
		if absolute != skill.snapshotPath || skill.Digest != skill.snapshotDigest || !os.SameFile(info, skill.directory) {
			return nil, ErrChanged
		}
	}
	indexedPath := filepath.ToSlash(relative)
	expected, exists := skill.records[indexedPath]
	if !exists || expected.kind == "directory" {
		return nil, fmt.Errorf("%w: resource is not in the loaded skill: %s", ErrUnsafePath, relative)
	}
	_, definitionRecord, err := consumeFile(context.Background(), root, "SKILL.md", MaxDefinitionBytes, io.Discard)
	if err != nil {
		return nil, err
	}
	if definitionRecord != skill.records["SKILL.md"] {
		return nil, fmt.Errorf("%w: SKILL.md", ErrChanged)
	}
	data, record, err := readBytes(context.Background(), root, indexedPath, maxBytes)
	if err != nil {
		return nil, err
	}
	if record != expected {
		return nil, fmt.Errorf("%w: %s", ErrChanged, relative)
	}
	return data, nil
}

func openSkillRoot(path string) (*os.Root, string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", fmt.Errorf("%w: skill directory is empty", ErrInvalid)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("%w: skill root must be a directory, not a symlink or file", ErrUnsafePath)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, "", err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		closeErr := root.Close()
		return nil, "", errors.Join(ErrChanged, err, closeErr)
	}
	return root, canonical, nil
}

func validateRelative(relative string) error {
	if relative == "" || len(relative) > 4096 || strings.ContainsAny(relative, "\\:\x00") || filepath.IsAbs(relative) {
		return fmt.Errorf("%w: expected a contained relative file path", ErrUnsafePath)
	}
	parts := strings.Split(relative, "/")
	if len(parts) > MaxPathDepth {
		return fmt.Errorf("%w: resource path is too deep", ErrLimit)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: path traversal is not allowed", ErrUnsafePath)
		}
	}
	return nil
}

func collectFiles(ctx context.Context, root *os.Root, directory string, skill *Skill, entries *int, total *int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := root.OpenFile(directory, os.O_RDONLY|nonblockingReadFlag(), 0)
	if err != nil {
		return fmt.Errorf("list skill directory: %w", err)
	}
	info, statErr := dir.Stat()
	if statErr != nil || !info.IsDir() {
		return errors.Join(ErrChanged, statErr, dir.Close())
	}
	children, readErr := dir.ReadDir(MaxEntries - *entries + 1)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(children)+*entries > MaxEntries {
		return fmt.Errorf("%w: more than %d directory entries", ErrLimit, MaxEntries)
	}
	slices.SortFunc(children, func(left, right fs.DirEntry) int { return strings.Compare(left.Name(), right.Name()) })
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		*entries = *entries + 1
		if *entries > MaxEntries {
			return fmt.Errorf("%w: more than %d directory entries", ErrLimit, MaxEntries)
		}
		relative := child.Name()
		if directory != "." {
			relative = directory + "/" + relative
		}
		if err := validateRelative(relative); err != nil {
			return err
		}
		info, err := root.Lstat(relative)
		if err != nil {
			return err
		}
		if info.IsDir() {
			skill.records[relative] = fileRecord{kind: "directory"}
			if err := collectFiles(ctx, root, relative, skill, entries, total); err != nil {
				return err
			}
			continue
		}
		if len(skill.Files) >= MaxFiles {
			return fmt.Errorf("%w: more than %d files", ErrLimit, MaxFiles)
		}
		limit := int64(MaxFileBytes)
		if relative == "SKILL.md" {
			limit = MaxDefinitionBytes
		}
		if remaining := int64(MaxSkillBytes) - *total; remaining < limit {
			limit = remaining
		}
		_, record, err := consumeFile(ctx, root, relative, limit, io.Discard)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", relative, err)
		}
		*total += record.size + int64(len(record.target))
		if *total > MaxSkillBytes {
			return fmt.Errorf("%w: skill exceeds %d bytes", ErrLimit, MaxSkillBytes)
		}
		skill.records[relative] = record
		skill.Files = append(skill.Files, relative)
	}
	return nil
}

func openRegular(root *os.Root, relative string, limit int64) (*os.File, os.FileInfo, fileRecord, error) {
	if err := validateResourceParents(root, relative); err != nil {
		return nil, nil, fileRecord{}, err
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, nil, fileRecord{}, err
	}
	record := fileRecord{kind: "file"}
	if info.Mode()&os.ModeSymlink != 0 {
		record.kind = "symlink"
		record.target, err = root.Readlink(relative)
		if err != nil {
			return nil, nil, fileRecord{}, err
		}
		record.target = filepath.ToSlash(record.target)
		if filepath.IsAbs(record.target) || strings.ContainsAny(record.target, "\\:\x00") {
			return nil, nil, fileRecord{}, fmt.Errorf("%w: symlink must have a contained relative target", ErrUnsafePath)
		}
		info, err = root.Stat(relative)
		if err != nil {
			return nil, nil, fileRecord{}, fmt.Errorf("%w: symlink target: %w", ErrUnsafePath, err)
		}
		if info.IsDir() {
			return nil, nil, fileRecord{}, fmt.Errorf("%w: directory symlinks", ErrUnsupported)
		}
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fileRecord{}, fmt.Errorf("%w: only regular resource files are supported", ErrUnsupported)
	}
	if info.Size() > limit {
		return nil, nil, fileRecord{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrLimit, relative, limit)
	}
	file, err := root.OpenFile(relative, os.O_RDONLY|nonblockingReadFlag(), 0)
	if err != nil {
		return nil, nil, fileRecord{}, fmt.Errorf("%w: open contained file: %w", ErrUnsafePath, err)
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > limit {
		closeErr := file.Close()
		return nil, nil, fileRecord{}, errors.Join(ErrChanged, err, closeErr)
	}
	record.executable = opened.Mode().Perm() & 0111
	return file, opened, record, nil
}

func validateResourceParents(root *os.Root, relative string) error {
	parts := strings.Split(relative, "/")
	for count := 1; count < len(parts); count++ {
		info, err := root.Lstat(strings.Join(parts[:count], "/"))
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: resource directory parents must not be symlinks", ErrUnsafePath)
		}
	}
	return nil
}

func consumeFile(ctx context.Context, root *os.Root, relative string, limit int64, output io.Writer) (int64, fileRecord, error) {
	if err := ctx.Err(); err != nil {
		return 0, fileRecord{}, err
	}
	file, before, record, err := openRegular(root, relative, limit)
	if err != nil {
		return 0, fileRecord{}, err
	}
	defer file.Close()
	hasher := sha256.New()
	writer := io.MultiWriter(hasher, output)
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return 0, fileRecord{}, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			record.size += int64(count)
			if record.size > limit {
				return 0, fileRecord{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrLimit, relative, limit)
			}
			if _, err := writer.Write(buffer[:count]); err != nil {
				return 0, fileRecord{}, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, fileRecord{}, readErr
		}
	}
	after, err := file.Stat()
	if err != nil {
		return 0, fileRecord{}, err
	}
	if before.Size() != after.Size() || record.size != before.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return 0, fileRecord{}, fmt.Errorf("%w: %s changed while reading", ErrChanged, relative)
	}
	if record.kind == "symlink" {
		target, err := root.Readlink(relative)
		if err != nil || filepath.ToSlash(target) != record.target {
			return 0, fileRecord{}, errors.Join(ErrChanged, err)
		}
	}
	copy(record.content[:], hasher.Sum(nil))
	if err := ctx.Err(); err != nil {
		return 0, fileRecord{}, err
	}
	return record.size, record, nil
}

func readBytes(ctx context.Context, root *os.Root, relative string, limit int64) ([]byte, fileRecord, error) {
	var output bytes.Buffer
	_, record, err := consumeFile(ctx, root, relative, limit, &output)
	if err != nil {
		return nil, fileRecord{}, err
	}
	return output.Bytes(), record, nil
}

func manifestDigest(records map[string]fileRecord) string {
	paths := make([]string, 0, len(records))
	for path := range records {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	hasher := sha256.New()
	writeHashPart(hasher, []byte("crude-agent-skill-v1"))
	for _, path := range paths {
		record := records[path]
		writeHashPart(hasher, []byte(path))
		writeHashPart(hasher, []byte(record.kind))
		writeHashPart(hasher, []byte(record.target))
		writeHashPart(hasher, binary.BigEndian.AppendUint64(nil, uint64(record.executable)))
		writeHashPart(hasher, binary.BigEndian.AppendUint64(nil, uint64(record.size)))
		writeHashPart(hasher, record.content[:])
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func writeHashPart(hasher hash.Hash, data []byte) {
	_, _ = hasher.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
	_, _ = hasher.Write(data)
}
