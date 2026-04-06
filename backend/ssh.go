package backend

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// SSHConfig holds connection parameters for the SSH backend.
type SSHConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	KeyFile  string
}

// SSH executes CRUDE operations on a remote host via SSH.
type SSH struct {
	cfg    SSHConfig
	client *gossh.Client
	mu     sync.Mutex
}

// NewSSH dials the remote host and returns a ready SSH backend.
func NewSSH(cfg SSHConfig) (*SSH, error) {
	s := &SSH{cfg: cfg}
	if err := s.dial(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SSH) dial() error {
	var authMethods []gossh.AuthMethod

	if s.cfg.Password != "" {
		authMethods = append(authMethods, gossh.Password(s.cfg.Password))
	}

	if s.cfg.KeyFile != "" {
		pem, err := os.ReadFile(s.cfg.KeyFile)
		if err != nil {
			return fmt.Errorf("read key file: %w", err)
		}
		signer, err := gossh.ParsePrivateKey(pem)
		if err != nil {
			return fmt.Errorf("parse private key: %w", err)
		}
		authMethods = append(authMethods, gossh.PublicKeys(signer))
	}

	port := s.cfg.Port
	if port == 0 {
		port = 22
	}

	clientCfg := &gossh.ClientConfig{
		User:            s.cfg.User,
		Auth:            authMethods,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", port))
	client, err := gossh.Dial("tcp", addr, clientCfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	if s.client != nil {
		s.client.Close()
	}
	s.client = client
	return nil
}

func (s *SSH) Type() string { return "ssh" }

// run executes a command via SSH, reconnecting once on failure.
func (s *SSH) run(cmd string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.client.NewSession()
	if err != nil {
		if rerr := s.dial(); rerr != nil {
			return "", fmt.Errorf("new session: %w (reconnect: %v)", err, rerr)
		}
		session, err = s.client.NewSession()
		if err != nil {
			return "", fmt.Errorf("new session after reconnect: %w", err)
		}
	}
	defer session.Close()

	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf
	if runErr := session.Run(cmd); runErr != nil {
		return buf.String(), runErr
	}
	return buf.String(), nil
}

// Execute runs a shell command on the remote host.
func (s *SSH) Execute(cmd string, opts ExecOptions) (string, error) {
	full := cmd
	if opts.CWD != "" {
		full = fmt.Sprintf("cd %q && %s", opts.CWD, cmd)
	}
	if opts.TimeoutSeconds > 0 {
		full = fmt.Sprintf("timeout %d %s", opts.TimeoutSeconds, full)
	}
	return s.run(full)
}

// ReadFile reads a remote file via cat.
func (s *SSH) ReadFile(path string) (string, error) {
	return s.run(fmt.Sprintf("cat %q", path))
}

// WriteFile writes content to a remote file using base64 transfer.
func (s *SSH) WriteFile(path, content string) error {
	dir := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		dir = path[:idx]
	}
	if dir != "" && dir != "." {
		if _, err := s.run(fmt.Sprintf("mkdir -p %q", dir)); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	_, err := s.run(fmt.Sprintf("echo '%s' | base64 -d > %q", encoded, path))
	return err
}

// EditFile performs an exact string replacement inside a remote file.
func (s *SSH) EditFile(path, oldStr, newStr string) (int, error) {
	content, err := s.ReadFile(path)
	if err != nil {
		return 0, err
	}
	count := strings.Count(content, oldStr)
	if count == 0 {
		return 0, fmt.Errorf("old_string not found in %s", path)
	}
	replaced := strings.ReplaceAll(content, oldStr, newStr)
	if err := s.WriteFile(path, replaced); err != nil {
		return 0, err
	}
	return count, nil
}

// DeleteFile removes a remote file.
func (s *SSH) DeleteFile(path string) error {
	_, err := s.run(fmt.Sprintf("rm -f %q", path))
	return err
}

// MoveFile moves src to dst on the remote host.
func (s *SSH) MoveFile(src, dst string) error {
	_, err := s.run(fmt.Sprintf("mv -f %q %q", src, dst))
	return err
}

// CopyFile copies src to dst on the remote host.
func (s *SSH) CopyFile(src, dst string) error {
	_, err := s.run(fmt.Sprintf("cp -rp %q %q", src, dst))
	return err
}

// ListDir returns metadata for entries in a remote directory.
func (s *SSH) ListDir(path string) ([]DirEntry, error) {
	// Use portable ls flags; --time-style is GNU-specific but widely available.
	out, err := s.run(fmt.Sprintf(`ls -la --time-style=+"%s" %q 2>/dev/null || ls -la %q`, `%Y-%m-%d %H:%M:%S`, path, path))
	if err != nil {
		return nil, err
	}
	return parseLsLa(out), nil
}

// parseLsLa parses the output of `ls -la` into DirEntry values.
func parseLsLa(out string) []DirEntry {
	var entries []DirEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "total") {
			continue
		}
		// Fields: mode links owner group size date time name [-> link]
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		mode := fields[0]
		name := strings.Join(fields[7:], " ")

		// Strip symlink target.
		isLink := strings.HasPrefix(mode, "l")
		if isLink {
			if idx := strings.Index(name, " -> "); idx >= 0 {
				name = name[:idx]
			}
		}

		// Skip . and ..
		if name == "." || name == ".." {
			continue
		}

		size, _ := strconv.ParseInt(fields[4], 10, 64)

		// Parse mod time: try "date time" format from --time-style, else just date.
		var modTime time.Time
		if len(fields) >= 8 {
			t, err := time.Parse("2006-01-02 15:04:05", fields[5]+" "+fields[6])
			if err != nil {
				t, _ = time.Parse("Jan _2 15:04", fields[5]+" "+fields[6]+" "+fields[7])
			}
			modTime = t
		}

		entries = append(entries, DirEntry{
			Name:    name,
			IsDir:   strings.HasPrefix(mode, "d"),
			IsLink:  isLink,
			Size:    size,
			ModTime: modTime,
			Mode:    mode,
		})
	}
	return entries
}

// GlobFiles returns paths under baseDir matching pattern on the remote host.
func (s *SSH) GlobFiles(baseDir, pattern string) ([]string, error) {
	if baseDir == "" {
		baseDir = "."
	}
	out, err := s.run(fmt.Sprintf("find %q -type f 2>/dev/null", baseDir))
	if err != nil && out == "" {
		return nil, err
	}
	// Filter in Go using shared matchGlobPattern.
	var matches []string
	for _, p := range strings.Split(strings.TrimSpace(out), "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Get relative path for matching.
		rel := strings.TrimPrefix(p, baseDir)
		rel = strings.TrimPrefix(rel, "/")
		if matchGlobPattern(pattern, rel) {
			matches = append(matches, p)
		}
	}
	return matches, nil
}

// GrepFiles searches file contents for pattern on the remote host.
func (s *SSH) GrepFiles(pattern, baseDir string, recursive bool) ([]GrepMatch, error) {
	if baseDir == "" {
		baseDir = "."
	}
	// Validate the pattern locally before sending.
	if _, err := regexp.Compile(pattern); err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	flags := "-n"
	if recursive {
		flags += "r"
	}
	cmd := fmt.Sprintf("grep %s -E -- %q %q 2>/dev/null", flags, pattern, baseDir)
	out, err := s.run(cmd)
	if err != nil && out == "" {
		// grep exits 1 when no matches — treat as empty.
		return nil, nil
	}

	var results []GrepMatch
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// Format: path:linenum:content (or just linenum:content for single-file grep)
		m := parseGrepLine(line, recursive)
		if m != nil {
			results = append(results, *m)
		}
	}
	return results, nil
}

// parseGrepLine parses a single grep output line.
func parseGrepLine(line string, hasPath bool) *GrepMatch {
	if hasPath {
		// path:linenum:content
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			return nil
		}
		num, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil
		}
		return &GrepMatch{Path: parts[0], LineNum: num, Line: parts[2]}
	}
	// linenum:content
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return nil
	}
	num, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil
	}
	return &GrepMatch{LineNum: num, Line: parts[1]}
}

// Close terminates the SSH connection.
func (s *SSH) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
}
