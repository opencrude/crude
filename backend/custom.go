package backend

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// CustomConfig holds settings for a custom command-based backend.
type CustomConfig struct {
	ExecCmd   string // Command template for execute: {cmd} is replaced with the command
	ReadCmd   string // Command template for read: {path} is replaced
	WriteCmd  string // Command template for write: {path} and {content} are replaced
	DeleteCmd string // Command template for delete: {path} is replaced
	ListCmd   string // Command template for list: {path} is replaced
	Shell     string // Shell to use (default: "sh")
	ShellFlag string // Shell flag (default: "-c")
}

// Custom executes CRUDE operations via user-defined command templates.
type Custom struct {
	cfg   CustomConfig
	shell string
	flag  string
}

// NewCustom creates a custom backend.
func NewCustom(cfg CustomConfig) *Custom {
	shell := cfg.Shell
	if shell == "" {
		shell = "sh"
	}
	flag := cfg.ShellFlag
	if flag == "" {
		flag = "-c"
	}
	return &Custom{cfg: cfg, shell: shell, flag: flag}
}

func (c *Custom) Type() string { return "custom" }

func (c *Custom) run(cmd string) (string, error) {
	c2 := exec.Command(c.shell, c.flag, cmd)
	var out bytes.Buffer
	c2.Stdout = &out
	c2.Stderr = &out
	err := c2.Run()
	return strings.TrimSpace(out.String()), err
}

func (c *Custom) replace(template, old, new string) string {
	return strings.ReplaceAll(template, old, new)
}

// Execute runs a command via the custom exec template.
func (c *Custom) Execute(cmd string, opts ExecOptions) (string, error) {
	if c.cfg.ExecCmd == "" {
		return "", fmt.Errorf("custom: no ExecCmd template configured")
	}
	resolved := c.replace(c.cfg.ExecCmd, "{cmd}", cmd)
	return c.run(resolved)
}

// ReadFile reads a file via the custom read template.
func (c *Custom) ReadFile(path string) (string, error) {
	if c.cfg.ReadCmd == "" {
		return "", fmt.Errorf("custom: no ReadCmd template configured")
	}
	resolved := c.replace(c.cfg.ReadCmd, "{path}", path)
	return c.run(resolved)
}

// WriteFile writes a file via the custom write template.
func (c *Custom) WriteFile(path, content string) error {
	if c.cfg.WriteCmd == "" {
		return fmt.Errorf("custom: no WriteCmd template configured")
	}
	resolved := c.replace(c.cfg.WriteCmd, "{path}", path)
	resolved = c.replace(resolved, "{content}", content)
	_, err := c.run(resolved)
	return err
}

// EditFile is not directly supported — use execute_command.
func (c *Custom) EditFile(path, oldStr, newStr string) (int, error) {
	return 0, fmt.Errorf("custom: EditFile not supported; use execute_command")
}

// DeleteFile deletes via the custom delete template.
func (c *Custom) DeleteFile(path string) error {
	if c.cfg.DeleteCmd == "" {
		return fmt.Errorf("custom: no DeleteCmd template configured")
	}
	resolved := c.replace(c.cfg.DeleteCmd, "{path}", path)
	_, err := c.run(resolved)
	return err
}

// MoveFile is not directly supported — use execute_command.
func (c *Custom) MoveFile(src, dst string) error {
	return fmt.Errorf("custom: MoveFile not supported; use execute_command")
}

// CopyFile is not directly supported — use execute_command.
func (c *Custom) CopyFile(src, dst string) error {
	return fmt.Errorf("custom: CopyFile not supported; use execute_command")
}

// ListDir lists via the custom list template.
func (c *Custom) ListDir(path string) ([]DirEntry, error) {
	if c.cfg.ListCmd == "" {
		return nil, fmt.Errorf("custom: no ListCmd template configured")
	}
	resolved := c.replace(c.cfg.ListCmd, "{path}", path)
	out, err := c.run(resolved)
	if err != nil {
		return nil, err
	}

	var entries []DirEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		isDir := strings.HasSuffix(line, "/")
		name := strings.TrimSuffix(line, "/")
		entries = append(entries, DirEntry{Name: name, IsDir: isDir})
	}
	return entries, nil
}

// GlobFiles is not supported for custom backends.
func (c *Custom) GlobFiles(baseDir, pattern string) ([]string, error) {
	return nil, fmt.Errorf("custom: GlobFiles not supported; use execute_command")
}

// GrepFiles is not supported for custom backends.
func (c *Custom) GrepFiles(pattern, baseDir string, recursive bool) ([]GrepMatch, error) {
	return nil, fmt.Errorf("custom: GrepFiles not supported; use execute_command")
}

// Ensure Custom implements Executor.
var _ Executor = (*Custom)(nil)
