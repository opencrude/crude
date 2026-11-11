package indentation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ec "github.com/editorconfig/editorconfig-core-go/v2"
)

// ReadProject uses the official EditorConfig parser, matcher, parent traversal,
// section precedence and unset semantics. It is an off-UI read; it executes no
// project code. A one-MiB configuration bound prevents accidental source-sized
// INI allocations. No bound truncates the result into a successful partial policy.
func ReadProject(ctx context.Context, path string) (Choices, error) {
	if path == "" {
		return Choices{}, nil
	}
	parser := configParser{ctx: ctx}
	config := ec.Config{Parser: parser}
	definition, err := config.Load(path)
	if err != nil {
		return Choices{}, err
	}
	if err = ctx.Err(); err != nil {
		return Choices{}, err
	}
	raw := definition.Raw
	value := Choices{}
	style := strings.ToLower(raw["indent_style"])
	switch style {
	case Tabs, Spaces:
		value.Style = style
	case "", ec.UnsetValue:
	default:
		return value, fmt.Errorf("unsupported indent_style %q in EditorConfig", style)
	}
	number := func(key string) (int, error) {
		text := raw[key]
		if text == "" || text == ec.UnsetValue {
			return 0, nil
		}
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > MaxWidth {
			return 0, fmt.Errorf("EditorConfig %s must be 1 to 32", key)
		}
		return n, nil
	}
	if raw["indent_size"] == Tabs {
		value.SizeIsTab = true
	} else if value.Size, err = number("indent_size"); err != nil {
		return Choices{}, err
	}
	if value.TabWidth, err = number("tab_width"); err != nil {
		return Choices{}, err
	}
	if _, present := raw["tab_width"]; !present && value.Size > 0 {
		value.TabWidth = value.Size
	}
	if _, present := raw["indent_size"]; !present && value.Style == Tabs {
		value.SizeIsTab = true
	}
	return value, nil
}

type configParser struct{ ctx context.Context }

func (p configParser) FnmatchCase(pattern, name string) (bool, error) {
	if err := p.ctx.Err(); err != nil {
		return false, err
	}
	return ec.FnmatchCase(pattern, name)
}
func (p configParser) ParseIni(path string) (*ec.Editorconfig, error) {
	value, warning, err := p.ParseIniGraceful(path)
	return value, errors.Join(warning, err)
}
func (p configParser) ParseIniGraceful(path string) (*ec.Editorconfig, error, error) {
	if err := p.ctx.Err(); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, nil, fmt.Errorf("read a regular EditorConfig file no larger than 1 MiB: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > 1<<20 {
		return nil, nil, fmt.Errorf("EditorConfig exceeds 1 MiB: %s", path)
	}
	if err = p.ctx.Err(); err != nil {
		return nil, nil, err
	}
	return ec.ParseGraceful(bytes.NewReader(data))
}

// ProjectFile follows the parser's own configuration filename contract.
func ProjectFile(path string) bool { return filepath.Base(path) == ec.ConfigNameDefault }
