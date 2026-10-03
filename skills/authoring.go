package skills

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"crude/fileio"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/unicode/norm"
)

type templateMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func Create(path, name, description string) (Skill, error) {
	if err := validateName(name); err != nil {
		return Skill{}, err
	}
	if err := validateDescription(description); err != nil {
		return Skill{}, err
	}
	header, err := yaml.Marshal(templateMetadata{Name: name, Description: description})
	if err != nil {
		return Skill{}, err
	}
	definition := bytes.Join([][]byte{[]byte("---\n"), header, []byte("---\n\n# " + name + "\n\nDescribe the steps, examples, and checks for this skill.\n")}, nil)
	return publishSkill(context.Background(), path, name, func(root *os.Root) error {
		return writeNewFile(context.Background(), root, "SKILL.md", definition, 0644)
	})
}

func MakeLocalCopy(ctx context.Context, skill Skill, destination string) (Skill, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	if skill.Digest == "" {
		return Skill{}, fmt.Errorf("%w: load the source skill before making a local copy", ErrInvalid)
	}
	name := filepath.Base(filepath.Clean(destination))
	if err := validateName(name); err != nil {
		return Skill{}, err
	}
	source, absolute, err := openSkillRoot(skill.Path)
	if err != nil {
		return Skill{}, err
	}
	defer source.Close()
	verified, err := loadRoot(ctx, source, absolute, filepath.Base(absolute), false)
	if err != nil {
		return Skill{}, err
	}
	if verified.Digest != skill.Digest {
		return Skill{}, ErrChanged
	}
	if verified.records["SKILL.md"].kind != "file" {
		return Skill{}, fmt.Errorf("%w: a copied SKILL.md must be a regular file", ErrUnsupported)
	}
	target, err := filepath.Abs(destination)
	if err != nil {
		return Skill{}, err
	}
	if containedPath(absolute, target) {
		return Skill{}, fmt.Errorf("%w: a local copy cannot be written inside its immutable source", ErrUnsafePath)
	}
	return publishSkill(ctx, target, name, func(root *os.Root) error {
		paths := make([]string, 0, len(verified.records))
		for relative := range verified.records {
			paths = append(paths, relative)
		}
		slices.Sort(paths)
		for _, relative := range paths {
			if err := ctx.Err(); err != nil {
				return err
			}
			record := verified.records[relative]
			if record.kind == "directory" {
				if err := root.MkdirAll(relative, 0755); err != nil {
					return err
				}
				continue
			}
			if err := root.MkdirAll(filepath.Dir(filepath.FromSlash(relative)), 0755); err != nil {
				return err
			}
			if record.kind == "symlink" {
				if err := root.Symlink(record.target, relative); err != nil {
					return err
				}
				continue
			}
			if relative == "SKILL.md" {
				definition, observed, err := readBytes(ctx, source, relative, MaxDefinitionBytes)
				if err != nil {
					return err
				}
				if observed != record {
					return ErrChanged
				}
				definition, err = renameDefinition(definition, verified.Name, name)
				if err != nil {
					return err
				}
				if err := writeNewFile(ctx, root, relative, definition, 0644|record.executable); err != nil {
					return err
				}
				continue
			}
			output, err := root.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644|record.executable)
			if err != nil {
				return err
			}
			_, observed, copyErr := consumeFile(ctx, source, relative, MaxFileBytes, output)
			if copyErr == nil && observed != record {
				copyErr = ErrChanged
			}
			if copyErr == nil {
				copyErr = output.Sync()
			}
			if err := errors.Join(copyErr, output.Close()); err != nil {
				return err
			}
		}
		current, err := loadRoot(ctx, source, absolute, filepath.Base(absolute), false)
		if err != nil {
			return err
		}
		if current.Digest != verified.Digest {
			return ErrChanged
		}
		return nil
	})
}

func publishSkill(ctx context.Context, destination, name string, populate func(*os.Root) error) (result Skill, resultErr error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return Skill{}, err
	}
	if norm.NFKC.String(filepath.Base(absolute)) != norm.NFKC.String(name) {
		return Skill{}, fmt.Errorf("%w: destination directory must match the skill name", ErrInvalid)
	}
	parentPath := filepath.Dir(absolute)
	canonicalParent, err := filepath.EvalSymlinks(parentPath)
	if err != nil {
		return Skill{}, err
	}
	if canonicalParent != parentPath {
		return Skill{}, fmt.Errorf("%w: destination parents must not be symlinks", ErrUnsafePath)
	}
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return Skill{}, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	parent, err := os.OpenRoot(canonicalParent)
	if err != nil {
		return Skill{}, err
	}
	defer parent.Close()
	parentIdentity, err := parent.Stat(".")
	if err != nil {
		return Skill{}, err
	}
	basename := filepath.Base(absolute)
	if _, err := parent.Lstat(basename); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return Skill{}, fmt.Errorf("create skill destination: %w", err)
	}
	temporary, err := makeStagingDirectory(parent)
	if err != nil {
		return Skill{}, err
	}
	ownedStaging, err := parent.Lstat(temporary)
	if err != nil {
		return Skill{}, err
	}
	published := false
	defer func() {
		if !published {
			current, err := parent.Lstat(temporary)
			if errors.Is(err, os.ErrNotExist) {
				return
			}
			if err != nil || !os.SameFile(ownedStaging, current) {
				resultErr = errors.Join(resultErr, ErrChanged, err)
				return
			}
			if err := parent.RemoveAll(temporary); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove incomplete skill staging: %w", err))
			}
		}
	}()
	staging, err := parent.OpenRoot(temporary)
	if err != nil {
		return Skill{}, err
	}
	openedStaging, err := staging.Stat(".")
	if err != nil || !os.SameFile(ownedStaging, openedStaging) {
		return Skill{}, errors.Join(ErrChanged, err, staging.Close())
	}
	if err := populate(staging); err != nil {
		return Skill{}, errors.Join(err, staging.Close())
	}
	staged, loadErr := loadRoot(ctx, staging, absolute, name, false)
	if err := errors.Join(loadErr, staging.Close()); err != nil {
		return Skill{}, err
	}
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	stagingIdentity, err := parent.Lstat(temporary)
	if err != nil || !stagingIdentity.IsDir() || !os.SameFile(staged.directory, stagingIdentity) {
		return Skill{}, errors.Join(ErrChanged, err)
	}
	currentParent, err := os.Lstat(parentPath)
	if err != nil || !currentParent.IsDir() || !os.SameFile(parentIdentity, currentParent) {
		return Skill{}, errors.Join(ErrChanged, err)
	}
	if err := publishDirectory(parent, temporary, basename); err != nil {
		return Skill{}, err
	}
	published = true
	return staged, nil
}

func makeStagingDirectory(parent *os.Root) (string, error) {
	for attempt := 0; attempt < 10; attempt++ {
		name, err := stagingName()
		if err != nil {
			return "", err
		}
		if err := parent.Mkdir(name, 0700); err == nil {
			return name, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("create skill staging: %w", os.ErrExist)
}

func writeNewFile(ctx context.Context, root *os.Root, relative string, data []byte, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := root.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func renameDefinition(definition []byte, previous, name string) ([]byte, error) {
	if previous == name {
		return definition, nil
	}
	header, body, err := splitDefinition(definition)
	if err != nil {
		return nil, err
	}
	mapping, err := decodeFrontmatter(header)
	if err != nil {
		return nil, err
	}
	for index := 0; index < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == "name" {
			mapping.Content[index+1].Value = name
			mapping.Content[index+1].Style = 0
		}
	}
	encoded, err := yaml.Marshal(mapping)
	if err != nil {
		return nil, err
	}
	return bytes.Join([][]byte{[]byte("---\n"), encoded, []byte("---\n"), body}, nil), nil
}

func containedPath(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
