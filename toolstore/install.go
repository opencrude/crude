package toolstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Install downloads, verifies and extracts one tool. When the same package
// and version is already installed with a matching digest, Install returns it
// and downloads nothing. Concurrent installs of the same package and version
// in this process install once. Every failure is recorded in the history.
func (s *Store) Install(ctx context.Context, r Request) (Tool, error) {
	if err := r.normalize(); err != nil {
		s.recordFailure(r, err)
		return Tool{}, err
	}
	unlock, err := s.lockKeys(ctx, []string{installKey(r.Package, r.Version)})
	if err != nil {
		err = fmt.Errorf("the install was cancelled: %w", err)
		s.recordFailure(r, err)
		return Tool{}, err
	}
	defer unlock()
	if tool, ok, err := s.installed(r); err != nil || ok {
		if err != nil {
			s.recordFailure(r, err)
		}
		return tool, err
	}
	tool, err := s.install(ctx, r)
	if err != nil {
		s.recordFailure(r, err)
		return Tool{}, err
	}
	return tool, nil
}

func (r *Request) normalize() error {
	if err := validComponent(r.Package); err != nil {
		return fmt.Errorf("package: %w", err)
	}
	if err := validComponent(r.Version); err != nil {
		return fmt.Errorf("version: %w", err)
	}
	if r.URL == "" {
		return errors.New("the download address is empty")
	}
	switch r.Archive {
	case ArchiveZip, ArchiveTarGz, ArchiveTarXz, ArchiveGz, ArchiveRaw:
	default:
		return fmt.Errorf("archive kind %q is not supported; use zip, tar.gz, tar.xz, gz or raw", r.Archive)
	}
	bin, err := cleanEntryName(r.Bin)
	if err != nil || bin == "" {
		return fmt.Errorf("executable path %q is not a contained file path", r.Bin)
	}
	if (r.Archive == ArchiveGz || r.Archive == ArchiveRaw) && strings.Contains(bin, "/") {
		return fmt.Errorf("executable %q must be a file name for a %s download", r.Bin, r.Archive)
	}
	r.Bin = bin
	r.ExpectedSHA256 = strings.ToLower(strings.TrimSpace(r.ExpectedSHA256))
	if r.ExpectedSHA256 != "" && !validDigest(r.ExpectedSHA256) {
		return errors.New("the expected digest is not a SHA-256 value in hex")
	}
	if r.MaxDownloadBytes <= 0 || r.MaxExtractedBytes <= 0 {
		return errors.New("the download and extraction budgets must be more than zero")
	}
	if r.MaxEntries < 0 {
		return errors.New("the archive entry budget must not be negative")
	}
	if r.MaxEntries == 0 {
		r.MaxEntries = DefaultMaxEntries
	}
	if strings.TrimSpace(r.InstalledBy) == "" || strings.TrimSpace(r.Approval) == "" {
		return errors.New("an install needs the installer and an approval reference")
	}
	r.Licenses = append([]string(nil), r.Licenses...)
	return nil
}

// installed returns the existing tool for the request's package and version.
func (s *Store) installed(r Request) (Tool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matches := s.matchesLocked(r.Package, r.Version)
	if len(matches) == 0 {
		return Tool{}, false, nil
	}
	for _, tool := range matches {
		if r.ExpectedSHA256 == "" && len(matches) == 1 || tool.SHA256 == r.ExpectedSHA256 {
			return s.withUsageLocked(tool), true, nil
		}
	}
	if len(matches) > 1 {
		return Tool{}, false, fmt.Errorf("%s %s has more than one installed build; give the expected digest", r.Package, r.Version)
	}
	return Tool{}, false, fmt.Errorf("%s %s is already installed with the digest %s, which differs from the expected digest. Remove the installed tool before you install a different build", r.Package, r.Version, matches[0].SHA256)
}

func (s *Store) install(ctx context.Context, r Request) (Tool, error) {
	staging, err := os.MkdirTemp(filepath.Join(s.root, stagingName), "install-")
	if err != nil {
		return Tool{}, fmt.Errorf("create staging folder: %w", err)
	}
	defer func() { _ = removeTree(staging) }()

	archivePath := filepath.Join(staging, "download")
	digest, _, err := s.download(ctx, r.URL, r.MaxDownloadBytes, archivePath)
	if err != nil {
		return Tool{}, err
	}
	origin := DigestFirstDownload
	if r.ExpectedSHA256 != "" {
		if digest != r.ExpectedSHA256 {
			return Tool{}, fmt.Errorf("the download digest %s differs from the expected digest %s", digest, r.ExpectedSHA256)
		}
		origin = DigestExpected
	}
	dirName := versionDir(r.Version, digest)
	id := r.Package + "/" + dirName
	final := filepath.Join(s.root, r.Package, dirName)
	if tool, ok, err := s.adopt(r.Package, dirName); ok || err != nil {
		return tool, err
	}

	tree := filepath.Join(staging, "tool")
	content := filepath.Join(tree, contentDir)
	if err := os.MkdirAll(content, 0o700); err != nil {
		return Tool{}, err
	}
	stats, err := extractArchive(ctx, r.Archive, archivePath, content, r.Bin, extractLimits{bytes: r.MaxExtractedBytes, entries: r.MaxEntries})
	if err != nil {
		return Tool{}, err
	}
	m := manifest{
		ID:           id,
		Package:      r.Package,
		Version:      r.Version,
		SHA256:       digest,
		DigestOrigin: origin,
		Executable:   r.Bin,
		Size:         stats.bytes,
		Files:        stats.files,
		Archive:      r.Archive,
		Source:       r.Source,
		URL:          r.URL,
		Licenses:     r.Licenses,
		InstalledAt:  s.now().UTC(),
		InstalledBy:  r.InstalledBy,
		Approval:     r.Approval,
	}
	if err := writeManifest(filepath.Join(tree, manifestName), m); err != nil {
		return Tool{}, err
	}
	if err := ctx.Err(); err != nil {
		return Tool{}, fmt.Errorf("the install was cancelled: %w", err)
	}
	// Seal everything below the tool folder. The tool folder itself stays
	// writable until it is in place, because moving a folder to another
	// parent needs write permission on it.
	if err := sealTree(tree); err != nil {
		return Tool{}, fmt.Errorf("make the tool read-only: %w", err)
	}
	packageDir := filepath.Join(s.root, r.Package)
	if err := s.moveIntoPlace(packageDir, tree, final); err != nil {
		if tool, ok, adoptErr := s.adopt(r.Package, dirName); ok || adoptErr != nil {
			return tool, adoptErr
		}
		return Tool{}, fmt.Errorf("move the tool into place: %w", err)
	}
	if err := sealDir(final); err != nil {
		return Tool{}, fmt.Errorf("make the tool read-only: %w", err)
	}
	if err := syncDir(packageDir); err != nil {
		return Tool{}, fmt.Errorf("save the tool folder: %w", err)
	}
	tool := toolFromManifest(final, m)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index[id] = tool
	if err := s.appendLocked(Event{
		Kind:     EventInstall,
		ToolID:   id,
		Package:  r.Package,
		Version:  r.Version,
		Detail:   fmt.Sprintf("Installed %d files (%d bytes) from %s. Digest %s (%s).", stats.files, stats.bytes, r.URL, digest, origin),
		By:       r.InstalledBy,
		Approval: r.Approval,
	}); err != nil {
		return s.withUsageLocked(tool), err
	}
	return s.withUsageLocked(tool), nil
}

// moveIntoPlace creates the package folder and renames the staged tree under
// the store mutex, so a removal cannot delete the empty package folder between
// the two steps.
func (s *Store) moveIntoPlace(packageDir, tree, final string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		return err
	}
	return os.Rename(tree, final)
}

// adopt returns the tool at <root>/<pkg>/<dir> when another install already
// put it there. A folder without a valid manifest is an error; the store does
// not overwrite it.
func (s *Store) adopt(pkg, dir string) (Tool, bool, error) {
	final := filepath.Join(s.root, pkg, dir)
	if _, err := os.Lstat(final); errors.Is(err, fs.ErrNotExist) {
		return Tool{}, false, nil
	}
	tool, err := s.readTool(pkg, dir)
	if err != nil {
		return Tool{}, false, fmt.Errorf("the folder %s exists but is not a valid tool. Inspect it and move it away, then install again", final)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index[tool.ID] = tool
	return s.withUsageLocked(tool), true, nil
}

func writeManifest(name string, m manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write tool manifest: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write tool manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("write tool manifest: %w", err)
	}
	return file.Close()
}

func (s *Store) recordFailure(r Request, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.appendLocked(Event{
		Kind:     EventInstallFailed,
		Package:  r.Package,
		Version:  r.Version,
		Detail:   cause.Error(),
		By:       r.InstalledBy,
		Approval: r.Approval,
	})
}
