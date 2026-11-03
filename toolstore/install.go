package toolstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"crude/fileio"
)

// Install downloads, verifies and extracts one tool. When the same package
// and version is already installed with a matching digest, Install returns it
// and downloads nothing. Concurrent installs of the same package and version
// in this process install once. A failure to record history is returned.
func (s *Store) Install(ctx context.Context, r Request) (Tool, error) {
	if err := r.normalize(); err != nil {
		return s.installFailure(ctx, r, Tool{}, err)
	}
	unlock, err := s.lockKeys(ctx, []string{installKey(r.Package, r.Version)})
	if err != nil {
		err = fmt.Errorf("the install was cancelled: %w", err)
		return s.installFailure(ctx, r, Tool{}, err)
	}
	defer unlock()
	if err := s.refusePendingPreparation(ctx, r.Package, r.Version); err != nil {
		return s.installFailure(ctx, r, Tool{}, err)
	}
	if tool, ok, err := s.installed(ctx, r); err != nil || ok {
		if err != nil {
			return s.installFailure(ctx, r, Tool{}, err)
		}
		if err := ctx.Err(); err != nil {
			return s.installFailure(ctx, r, Tool{}, err)
		}
		return tool, nil
	}
	tool, err := s.install(ctx, r)
	if err != nil {
		return s.installFailure(ctx, r, tool, err)
	}
	return tool, nil
}

func (s *Store) installFailure(ctx context.Context, r Request, tool Tool, cause error) (Tool, error) {
	err := errors.Join(cause, s.recordFailure(ctx, r, cause))
	var unconfirmed *PublicationUnconfirmedError
	if errors.As(cause, &unconfirmed) {
		// Failure-history I/O can itself change or lose the pending record.
		// Re-establish the refusal after that I/O, without overwriting a
		// replacement record made by another writer.
		return s.unconfirmedPublication(tool, unconfirmed.ToolID,
			filepath.Join(s.root, r.Package), filepath.Base(unconfirmed.ToolID), err)
	}
	return tool, err
}

// PublicationUnconfirmedError means a rename was attempted or succeeded, but
// its complete durable outcome is not established. The caller must inspect
// ToolID and the store before taking another action; it must not replay.
type PublicationUnconfirmedError struct {
	ToolID string
	Err    error
}

func (e *PublicationUnconfirmedError) Error() string {
	return fmt.Sprintf("tool publication %s is not confirmed; inspect the store before trying again: %v", e.ToolID, e.Err)
}

func (e *PublicationUnconfirmedError) Unwrap() error { return e.Err }

// EvaluationUnverifiedError means the exact staged process could not be joined.
// Its executable bytes and pre-launch durable marker must remain for inspection.
// It is not a successful check, an installed tool, or permission to retry.
type EvaluationUnverifiedError struct{ Err error }

func (e *EvaluationUnverifiedError) Error() string {
	return fmt.Sprintf("the staged tool check could not be stopped; inspect the tools before trying again: %v", e.Err)
}
func (e *EvaluationUnverifiedError) Unwrap() error { return e.Err }

// PreparationUnverifiedError retains the exact staging tree because a
// preparation process or its effects could not be retired with certainty.
type PreparationUnverifiedError struct{ Err error }

func (e *PreparationUnverifiedError) Error() string {
	return fmt.Sprintf("the tool preparation needs inspection before another attempt: %v", e.Err)
}
func (e *PreparationUnverifiedError) Unwrap() error { return e.Err }

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
		if r.Prepare != nil {
			return errors.New("a download cannot have a preparation callback")
		}
	case ArchivePrepared:
		if r.Prepare == nil || strings.TrimSpace(r.ExpectedSHA256) != "" {
			return errors.New("a prepared tree needs a preparation callback and has no expected download digest")
		}
	default:
		return fmt.Errorf("archive kind %q is not supported", r.Archive)
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
	if r.MaxDownloadBytes <= 0 || r.MaxExtractedBytes <= 0 ||
		r.MaxDownloadBytes == math.MaxInt64 || r.MaxExtractedBytes == math.MaxInt64 {
		return errors.New("the download and extraction budgets must be positive and bounded")
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
func (s *Store) installed(ctx context.Context, r Request) (Tool, bool, error) {
	if err := s.mu.LockContext(ctx); err != nil {
		return Tool{}, false, err
	}
	defer s.mu.Unlock()
	prefix := r.Package + "/" + r.Version + "-"
	for id := range s.unconfirmed {
		if strings.HasPrefix(id, prefix) && len(id) == len(prefix)+12 {
			return Tool{}, false, &PublicationUnconfirmedError{ToolID: id, Err: errors.New("the tool publication needs inspection before another install")}
		}
	}
	pending, err := pendingVersion(ctx, filepath.Join(s.root, r.Package), r.Version)
	if err != nil {
		return Tool{}, false, err
	}
	if pending != "" {
		return Tool{}, false, &PublicationUnconfirmedError{ToolID: r.Package + "/" + pending, Err: errors.New("the tool publication needs inspection before another install")}
	}
	matches := s.matchesLocked(r.Package, r.Version)
	if len(matches) == 0 {
		return Tool{}, false, nil
	}
	for _, tool := range matches {
		if (r.ExpectedSHA256 == "" && len(matches) == 1 || tool.SHA256 == r.ExpectedSHA256) && sameArtifact(tool, r) {
			return s.withUsageLocked(tool), true, nil
		}
	}
	if len(matches) > 1 {
		return Tool{}, false, fmt.Errorf("%s %s has more than one installed build; give the expected digest", r.Package, r.Version)
	}
	return Tool{}, false, fmt.Errorf("%s %s is already installed with a different artifact identity or digest. Remove the installed tool before you install a different build", r.Package, r.Version)
}

func sameArtifact(tool Tool, r Request) bool {
	return tool.Package == r.Package && tool.Version == r.Version &&
		tool.Source == r.Source && tool.URL == r.URL && tool.Archive == r.Archive &&
		tool.Bin == r.Bin && tool.Command == r.Command &&
		(r.ExpectedSHA256 == "" || tool.SHA256 == r.ExpectedSHA256)
}

func (s *Store) install(ctx context.Context, r Request) (Tool, error) {
	staging, err := os.MkdirTemp(filepath.Join(s.root, stagingName), "install-")
	if err != nil {
		return Tool{}, fmt.Errorf("create staging folder: %w", err)
	}
	stageIdentity, err := os.Lstat(staging)
	if err != nil {
		return Tool{}, fmt.Errorf("inspect staging folder: %w", err)
	}
	retain := false
	defer func() {
		if !retain && sameStagingFolder(staging, stageIdentity) {
			_ = removeTree(staging)
		}
	}()

	tree := filepath.Join(staging, "tool")
	content := filepath.Join(tree, contentDir)
	if err := os.MkdirAll(content, 0o700); err != nil {
		return Tool{}, err
	}
	archivePath := filepath.Join(staging, "download")
	var digest, origin string
	var stats extractStats
	if r.Archive == ArchivePrepared {
		digest, stats, err = s.prepareTree(ctx, r, staging, stageIdentity, content, &retain)
		origin = DigestPreparedTree
	} else {
		digest, _, err = s.download(ctx, r.URL, r.MaxDownloadBytes, archivePath)
		origin = DigestFirstDownload
		if err == nil && r.ExpectedSHA256 != "" {
			if digest != r.ExpectedSHA256 {
				return Tool{}, fmt.Errorf("the download digest %s differs from the expected digest %s", digest, r.ExpectedSHA256)
			}
			origin = DigestExpected
		}
	}
	if err != nil {
		return Tool{}, err
	}
	if !sameStagingFolder(staging, stageIdentity) {
		return Tool{}, errors.New("the staging folder changed; inspect the tools before trying again")
	}
	dirName := versionDir(r.Version, digest)
	id := r.Package + "/" + dirName
	final := filepath.Join(s.root, r.Package, dirName)
	if tool, ok, err := s.adopt(ctx, r.Package, dirName); ok || err != nil {
		if ok && !sameArtifact(tool, r) {
			return Tool{}, errors.New("installed artifact identity differs from the reviewed request")
		}
		if err == nil && ctx.Err() != nil {
			return Tool{}, ctx.Err()
		}
		return tool, err
	}

	if r.Archive != ArchivePrepared {
		stats, err = extractArchive(ctx, r.Archive, archivePath, content, r.Bin, extractLimits{bytes: r.MaxExtractedBytes, entries: r.MaxEntries})
		if err != nil {
			return Tool{}, err
		}
	}
	if !sameStagingFolder(staging, stageIdentity) {
		return Tool{}, errors.New("the staging folder changed; inspect the tools before trying again")
	}
	m := manifest{
		ID:           id,
		Package:      r.Package,
		Version:      r.Version,
		SHA256:       digest,
		DigestOrigin: origin,
		Executable:   r.Bin,
		Command:      r.Command,
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
	if !sameStagingFolder(staging, stageIdentity) {
		return Tool{}, errors.New("the staging folder changed; inspect the tools before trying again")
	}
	if r.Evaluate != nil {
		// Before launch, a crash or panic must leave a durable refusal to prune.
		marker := filepath.Join(staging, evaluationPendingName)
		if err := writeManifest(marker, m); err != nil {
			return Tool{}, fmt.Errorf("save staged tool check: %w", err)
		}
		if err := syncDir(staging); err != nil {
			retain = true
			return Tool{}, fmt.Errorf("save staged tool check folder: %w", err)
		}
		retain = true
		if err := syncDir(filepath.Dir(staging)); err != nil {
			return Tool{}, fmt.Errorf("save staged tool check parent: %w", err)
		}
		if err := ctx.Err(); err != nil {
			retain = false // no process has been constructed
			return Tool{}, err
		}
		err := r.Evaluate(ctx, toolFromManifest(tree, m))
		var unverified *EvaluationUnverifiedError
		if errors.As(err, &unverified) {
			return Tool{}, err
		}
		// Callback return now positively verifies joined teardown, even on failure.
		retain = false
		if err != nil {
			return Tool{}, fmt.Errorf("check staged tool: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Tool{}, err
		}
		if err := os.Remove(marker); err != nil {
			retain = true
			return Tool{}, fmt.Errorf("finish staged tool check: %w", err)
		}
		if err := syncDir(staging); err != nil {
			retain = true
			return Tool{}, fmt.Errorf("save staged tool check completion: %w", err)
		}
	}
	packageDir := filepath.Join(s.root, r.Package)
	if r.PreparePublication != nil {
		if err := r.PreparePublication(ctx); err != nil {
			return Tool{}, fmt.Errorf("prepare tool publication: %w", err)
		}
	}
	if !sameStagingFolder(staging, stageIdentity) {
		return Tool{}, errors.New("the staging folder changed; inspect the tools before trying again")
	}
	if r.Archive == ArchivePrepared {
		// Evaluation and preflight may run code. Read-only permissions do not
		// prevent the owner from changing them, so bind publication to the exact
		// checked bytes and manifest, not merely the earlier prepared digest.
		if err := sealTree(tree); err != nil {
			return Tool{}, fmt.Errorf("make the checked tool read-only: %w", err)
		}
		current, err := preparedTreeDigest(ctx, content, extractLimits{bytes: r.MaxExtractedBytes, entries: r.MaxEntries})
		if err != nil {
			return Tool{}, fmt.Errorf("recheck the prepared tool: %w", err)
		}
		if current != digest {
			return Tool{}, errors.New("the prepared tool changed after its check; review it again")
		}
		expectedManifest, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return Tool{}, err
		}
		actualManifest, err := fileio.ReadRegularNoFollow(ctx, filepath.Join(tree, manifestName), maxManifestBytes)
		if err != nil || !bytes.Equal(actualManifest, append(expectedManifest, '\n')) {
			return Tool{}, errors.New("the prepared tool manifest changed after its check; review it again")
		}
	}
	attempted, reservation, err := s.moveIntoPlace(ctx, r.AdmitPublication, packageDir, tree, final)
	if err != nil {
		if !attempted {
			return Tool{}, fmt.Errorf("admit tool publication: %w", err)
		}
		if fileio.WriteCommitted(err) {
			return s.unconfirmedPublication(toolFromManifest(final, m), id, packageDir, dirName, err)
		}
		return s.unconfirmedPublication(Tool{}, id, packageDir, dirName, fmt.Errorf("move the tool into place: %w", err))
	}
	tool := toolFromManifest(final, m)
	if err := sealDir(final); err != nil {
		return s.unconfirmedPublication(tool, id, packageDir, dirName, fmt.Errorf("make the tool read-only: %w", err))
	}
	if err := syncDir(final); err != nil {
		return s.unconfirmedPublication(tool, id, packageDir, dirName, fmt.Errorf("save the tool folder: %w", err))
	}
	if err := syncDir(packageDir); err != nil {
		return s.unconfirmedPublication(tool, id, packageDir, dirName, fmt.Errorf("save the tool folder: %w", err))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.appendLocked(Event{
		Kind:     EventInstall,
		ToolID:   id,
		Package:  r.Package,
		Version:  r.Version,
		Detail:   fmt.Sprintf("Installed %d files (%d bytes) from %s. Digest %s (%s).", stats.files, stats.bytes, r.URL, digest, origin),
		By:       r.InstalledBy,
		Approval: r.Approval,
	}); err != nil {
		return s.unconfirmedPublicationLocked(s.withUsageLocked(tool), id, packageDir, dirName, err)
	}
	if err := finishPublication(packageDir, reservation); err != nil {
		return s.unconfirmedPublicationLocked(s.withUsageLocked(tool), id, packageDir, dirName, err)
	}
	delete(s.unconfirmed, id)
	s.index[id] = tool
	return s.withUsageLocked(tool), nil
}

func (s *Store) unconfirmedPublication(tool Tool, id, packageDir, name string, cause error) (Tool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unconfirmedPublicationLocked(tool, id, packageDir, name, cause)
}

// Preserve both the current-process fence and, when possible, a durable
// refusal for every error after publication was attempted.
func (s *Store) unconfirmedPublicationLocked(tool Tool, id, packageDir, name string, cause error) (Tool, error) {
	s.unconfirmed[id] = true
	return tool, &PublicationUnconfirmedError{
		ToolID: id, Err: errors.Join(cause, preservePublication(packageDir, name)),
	}
}

// moveIntoPlace creates the package folder and renames the staged tree under
// the store mutex, so a removal cannot delete the empty package folder between
// the two steps. The caller's short admission check runs after filesystem
// preparation and before rename. It must not call back into the store.
func (s *Store) moveIntoPlace(ctx context.Context, admit func(context.Context) error, packageDir, tree, final string) (bool, *publicationReservation, error) {
	if err := s.mu.LockContext(ctx); err != nil {
		return false, nil, err
	}
	defer s.mu.Unlock()
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		return false, nil, err
	}
	if err := syncDir(s.root); err != nil {
		return false, nil, err
	}
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	name := filepath.Base(final)
	reservation, err := beginPublication(packageDir, name)
	if err != nil {
		return false, nil, err
	}
	if err := ctx.Err(); err != nil {
		return false, nil, errors.Join(err, finishPublication(packageDir, reservation))
	}
	if admit != nil {
		if err := admit(ctx); err != nil {
			return false, nil, errors.Join(err, finishPublication(packageDir, reservation))
		}
	}
	// Scans must not offer an artifact between rename and durable completion,
	// even if its on-disk record is concurrently lost.
	s.unconfirmed[filepath.Base(packageDir)+"/"+name] = true
	return true, reservation, fileio.MoveDirectoryNoReplace(tree, final)
}

// adopt returns the tool at <root>/<pkg>/<dir> when another install already
// put it there. A folder without a valid manifest is an error; the store does
// not overwrite it.
func (s *Store) adopt(ctx context.Context, pkg, dir string) (Tool, bool, error) {
	if err := ctx.Err(); err != nil {
		return Tool{}, false, err
	}
	final := filepath.Join(s.root, pkg, dir)
	if _, err := os.Lstat(final); errors.Is(err, fs.ErrNotExist) {
		return Tool{}, false, nil
	}
	if err := s.mu.LockContext(ctx); err != nil {
		return Tool{}, false, err
	}
	defer s.mu.Unlock()
	if s.unconfirmed[pkg+"/"+dir] {
		return Tool{}, false, errors.New("the tool publication needs inspection before another install")
	}
	tool, err := s.readToolContext(ctx, pkg, dir)
	if err != nil {
		return Tool{}, false, fmt.Errorf("the folder %s exists but is not a valid tool. Inspect it and move it away, then install again: %w", final, err)
	}
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

func (s *Store) recordFailure(ctx context.Context, r Request, cause error) error {
	if err := ctx.Err(); err != nil {
		// Cancellation must not wait behind another store operation. Preserve
		// the audit when the lock is free, and report when it cannot be taken.
		if !s.mu.TryLock() {
			return fmt.Errorf("record install failure: %w", err)
		}
	} else if err := s.mu.LockContext(ctx); err != nil {
		return fmt.Errorf("record install failure: %w", err)
	}
	defer s.mu.Unlock()
	if err := s.appendLocked(Event{
		Kind:     EventInstallFailed,
		Package:  r.Package,
		Version:  r.Version,
		Detail:   cause.Error(),
		By:       r.InstalledBy,
		Approval: r.Approval,
	}); err != nil {
		return fmt.Errorf("record install failure: %w", err)
	}
	return nil
}
