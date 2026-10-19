package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"crude/fileio"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

var ErrGitReviewStale = errors.New("git review is stale; refresh and review the changed versions before continuing")

const gitComparisonLimit int64 = 64 << 20

type GitComparison struct {
	Path           string
	AbsolutePath   string
	RepositoryRoot string
	OriginalPath   string
	BaseLabel      string
	TargetLabel    string
	Before         string
	After          string
	Fingerprint    string
	Area           GitChangeArea
	Binary         bool
	Untracked      bool
	Deleted        bool
	BeforePresent  bool
	AfterPresent   bool
	BeforeMode     uint32
	AfterMode      uint32
	Historical     bool
}

type GitCommitReview struct {
	RepositoryRoot string
	Head           string
	Fingerprint    string
	Comparisons    []GitComparison
}

type GitCommitEvidence struct {
	Commit      GitCommitSummary
	Message     string
	Comparisons []GitComparison
}

type gitReviewState struct {
	repo        *git.Repository
	root        string
	idx         *index.Index
	head        *plumbing.Reference
	headRaw     *plumbing.Reference
	headTree    *object.Tree
	fingerprint string
	status      git.Status // lazily read once per state for rename sources
}

type gitReviewFile struct {
	text    string
	mode    filemode.FileMode
	present bool
}

func (s *GitService) Compare(ctx context.Context, change GitChange) (GitComparison, error) {
	if s == nil {
		return GitComparison{}, errors.New("git service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return GitComparison{}, err
	}
	if err := s.lock(ctx); err != nil {
		return GitComparison{}, err
	}
	defer s.unlock()
	state, err := s.readReviewState(ctx)
	if err != nil {
		return GitComparison{}, err
	}
	defer state.close()
	return state.compare(ctx, change)
}

func (state *gitReviewState) close() { closeGitRepository(state.repo) }

func (s *GitService) readReviewState(ctx context.Context) (_ *gitReviewState, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repo, root, err := s.open()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			closeGitRepository(repo)
		}
	}()
	idx, err := repo.Storer.Index()
	if err != nil {
		return nil, fmt.Errorf("read Git index: %w", err)
	}
	raw, err := repo.Reference(plumbing.HEAD, false)
	if err != nil {
		return nil, fmt.Errorf("read Git HEAD: %w", err)
	}
	state := &gitReviewState{repo: repo, root: root, idx: idx, headRaw: raw}
	head, err := repo.Head()
	if err != nil && !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, fmt.Errorf("resolve Git HEAD: %w", err)
	}
	if err == nil {
		state.head = head
		commit, err := repo.CommitObject(head.Hash())
		if err != nil {
			return nil, fmt.Errorf("read HEAD commit: %w", err)
		}
		state.headTree, err = commit.Tree()
		if err != nil {
			return nil, fmt.Errorf("read HEAD tree: %w", err)
		}
	}
	var encoded bytes.Buffer
	if err := index.NewEncoder(&encoded).Encode(idx); err != nil {
		return nil, fmt.Errorf("encode Git index: %w", err)
	}
	headIdentity := raw.String()
	if head != nil {
		headIdentity += "\n" + head.String()
	}
	state.fingerprint = gitReviewDigest(root, headIdentity, encoded.String())
	return state, ctx.Err()
}

func gitReviewDigest(parts ...string) string {
	digest := sha256.New()
	for _, part := range parts {
		fmt.Fprintf(digest, "%d:", len(part))
		_, _ = io.WriteString(digest, part)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func (state *gitReviewState) compare(ctx context.Context, change GitChange) (GitComparison, error) {
	rel, err := gitReviewPath(state.root, change.Path, change.AbsolutePath)
	if err != nil {
		return GitComparison{}, err
	}
	if change.Area == GitAreaConflict {
		return GitComparison{}, errors.New("unresolved conflict: inspect the working file; staging requires resolving the index conflict first")
	}
	beforePath := rel
	if change.Code == "R" || change.Code == "C" {
		status, err := state.worktreeStatus(ctx)
		if err != nil {
			return GitComparison{}, err
		}
		if current := status[rel]; current != nil && current.Extra != "" {
			beforePath, err = gitReviewPath(state.root, current.Extra, "")
			if err != nil {
				return GitComparison{}, err
			}
		} else {
			return GitComparison{}, errors.New("rename source is unavailable; refresh the change list before reviewing")
		}
	}
	var before, after gitReviewFile
	baseLabel, targetLabel := "Index", "Working tree (disk)"
	switch change.Area {
	case GitAreaStaged:
		before, err = state.treeFile(ctx, state.headTree, beforePath)
		if err == nil {
			after, err = state.indexFile(ctx, rel)
		}
		baseLabel, targetLabel = "HEAD", "Index (staged)"
		if state.head == nil {
			baseLabel = "Empty tree (no commits)"
		}
	case GitAreaWorking, GitAreaUntracked:
		// Both sides in working-tree form: disk bytes against the index as Git
		// would check it out here, never raw repository-form bytes.
		before, err = state.indexCheckoutFile(ctx, beforePath)
		if err == nil {
			after, err = gitReviewDiskFile(ctx, state.root, rel)
		}
	default:
		return GitComparison{}, errors.New("unknown Git comparison area")
	}
	if err != nil {
		return GitComparison{}, err
	}
	if !before.present && !after.present {
		return GitComparison{}, fmt.Errorf("comparison unavailable: %s exists in neither compared version", rel)
	}
	comparison := gitReviewComparison(state.root, rel, change.Area, before, after, baseLabel, targetLabel)
	if beforePath != rel {
		comparison.OriginalPath = beforePath
	}
	comparison.Fingerprint = gitReviewDigest(state.fingerprint, gitComparisonIdentity(comparison))
	return comparison, ctx.Err()
}

func (state *gitReviewState) worktreeStatus(ctx context.Context) (git.Status, error) {
	if state.status != nil {
		return state.status, nil
	}
	status, err := ReadGitStatus(ctx, state.root)
	if err != nil {
		return nil, err
	}
	state.status = status
	return status, nil
}

func gitComparisonIdentity(comparison GitComparison) string {
	return gitReviewDigest(comparison.RepositoryRoot, comparison.Path, comparison.OriginalPath,
		comparison.Before, comparison.After,
		fmt.Sprintf("%d:%t:%t:%d:%d", comparison.Area, comparison.BeforePresent, comparison.AfterPresent, comparison.BeforeMode, comparison.AfterMode))
}

func gitReviewComparison(root, rel string, area GitChangeArea, before, after gitReviewFile, base, target string) GitComparison {
	return GitComparison{
		RepositoryRoot: root, Path: rel, AbsolutePath: filepath.Join(root, filepath.FromSlash(rel)),
		Area: area, BaseLabel: base, TargetLabel: target, Before: before.text, After: after.text,
		BeforePresent: before.present, AfterPresent: after.present, BeforeMode: uint32(before.mode), AfterMode: uint32(after.mode),
		Binary:    fileio.IsBinary([]byte(before.text)) || fileio.IsBinary([]byte(after.text)) || !utf8.ValidString(before.text) || !utf8.ValidString(after.text),
		Untracked: area == GitAreaUntracked, Deleted: before.present && !after.present,
	}
}

func gitReviewPath(root, rel, absolute string) (string, error) {
	if rel == "" && absolute != "" {
		var err error
		rel, err = filepath.Rel(root, absolute)
		if err != nil {
			return "", err
		}
	}
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == "." || !filepath.IsLocal(filepath.FromSlash(rel)) || path.Clean(rel) != rel {
		return "", errors.New("comparison requires an exact repository-relative file path")
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == ".." || strings.EqualFold(segment, ".git") || strings.ContainsRune(segment, '\x00') {
			return "", errors.New("git review path is outside repository file content")
		}
	}
	if absolute != "" && filepath.Clean(absolute) != filepath.Join(root, filepath.FromSlash(rel)) {
		return "", errors.New("git review path does not belong to this repository")
	}
	return rel, nil
}

func (state *gitReviewState) blob(ctx context.Context, hash plumbing.Hash, mode filemode.FileMode) (gitReviewFile, error) {
	if err := ctx.Err(); err != nil {
		return gitReviewFile{}, err
	}
	if mode == filemode.Submodule {
		return gitReviewFile{text: "Submodule commit " + hash.String() + "\n", mode: mode, present: true}, nil
	}
	blob, err := state.repo.BlobObject(hash)
	if err != nil {
		return gitReviewFile{}, fmt.Errorf("read Git blob: %w", err)
	}
	reader, err := blob.Reader()
	if err != nil {
		return gitReviewFile{}, err
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, gitComparisonLimit+1))
	closeErr := reader.Close()
	if readErr != nil {
		return gitReviewFile{}, readErr
	}
	if closeErr != nil {
		return gitReviewFile{}, closeErr
	}
	if int64(len(content)) > gitComparisonLimit {
		return gitReviewFile{}, errors.New("git comparison exceeds the 64 MiB review limit")
	}
	return gitReviewFile{text: string(content), mode: mode, present: true}, ctx.Err()
}

func (state *gitReviewState) treeFile(ctx context.Context, tree *object.Tree, rel string) (gitReviewFile, error) {
	if tree == nil {
		return gitReviewFile{}, nil
	}
	entry, err := tree.FindEntry(rel)
	if errors.Is(err, object.ErrEntryNotFound) || errors.Is(err, object.ErrDirectoryNotFound) {
		return gitReviewFile{}, nil
	}
	if err != nil {
		return gitReviewFile{}, fmt.Errorf("read Git tree path %s: %w", rel, err)
	}
	return state.blob(ctx, entry.Hash, entry.Mode)
}

func (state *gitReviewState) indexFile(ctx context.Context, rel string) (gitReviewFile, error) {
	found, err := state.indexEntry(rel)
	if err != nil || found == nil {
		return gitReviewFile{}, err
	}
	return state.blob(ctx, found.Hash, found.Mode)
}

// indexCheckoutFile is the index entry in the working-tree form Git would
// check out at rel. Links and submodules have no content conversion.
func (state *gitReviewState) indexCheckoutFile(ctx context.Context, rel string) (gitReviewFile, error) {
	found, err := state.indexEntry(rel)
	if err != nil || found == nil {
		return gitReviewFile{}, err
	}
	if found.Mode == filemode.Symlink || found.Mode == filemode.Submodule {
		return state.blob(ctx, found.Hash, found.Mode)
	}
	text, err := gitCheckoutText(ctx, state.root, rel, found.Hash.String())
	if err != nil {
		return gitReviewFile{}, err
	}
	return gitReviewFile{text: text, mode: found.Mode, present: true}, ctx.Err()
}

func (state *gitReviewState) indexEntry(rel string) (*index.Entry, error) {
	var found *index.Entry
	for _, entry := range state.idx.Entries {
		if entry.Name != rel {
			continue
		}
		if entry.Stage != 0 || found != nil {
			return nil, fmt.Errorf("%s has unresolved index stages; resolve the conflict before staging", rel)
		}
		if entry.IntentToAdd {
			return nil, nil
		}
		found = entry
	}
	return found, nil
}

func gitReviewDiskFile(ctx context.Context, root, rel string) (gitReviewFile, error) {
	segments := strings.Split(rel, "/")
	parent := root
	for _, segment := range segments[:len(segments)-1] {
		parent = filepath.Join(parent, segment)
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			return gitReviewFile{}, nil
		}
		if err != nil {
			return gitReviewFile{}, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return gitReviewFile{}, errors.New("git review refuses a symlink or non-directory parent")
		}
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return gitReviewFile{}, nil
	}
	if err != nil {
		return gitReviewFile{}, err
	}
	mode, err := filemode.NewFromOSFileMode(info.Mode())
	if err != nil {
		return gitReviewFile{}, err
	}
	if mode == filemode.Symlink {
		target, err := os.Readlink(abs)
		return gitReviewFile{text: target, mode: mode, present: true}, err
	}
	if !info.Mode().IsRegular() {
		return gitReviewFile{}, errors.New("git review supports regular files and symbolic links, not directories or devices")
	}
	if info.Size() > gitComparisonLimit {
		return gitReviewFile{}, errors.New("git comparison exceeds the 64 MiB review limit")
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return gitReviewFile{}, err
	}
	if int64(len(content)) > gitComparisonLimit {
		return gitReviewFile{}, errors.New("git comparison exceeds the 64 MiB review limit")
	}
	return gitReviewFile{text: string(content), mode: mode, present: true}, ctx.Err()
}

func (s *GitService) StageReviewed(ctx context.Context, comparison GitComparison) error {
	return s.mutateReviewed(ctx, comparison, true, func() (string, filemode.FileMode, bool, error) {
		return comparison.After, filemode.FileMode(comparison.AfterMode), comparison.AfterPresent, nil
	})
}

func (s *GitService) UnstageReviewed(ctx context.Context, comparison GitComparison) error {
	return s.mutateReviewed(ctx, comparison, false, func() (string, filemode.FileMode, bool, error) {
		return comparison.Before, filemode.FileMode(comparison.BeforeMode), comparison.BeforePresent, nil
	})
}

// mutateReviewed replaces one index entry with target bytes, but only while
// the painted comparison is still exactly the repository's state.
func (s *GitService) mutateReviewed(ctx context.Context, comparison GitComparison, stage bool, target func() (string, filemode.FileMode, bool, error)) error {
	if s == nil {
		return errors.New("git service is unavailable")
	}
	if comparison.Fingerprint == "" || comparison.Historical || comparison.OriginalPath != "" {
		return errors.New("this comparison cannot mutate the index; refresh and review an individual file change")
	}
	if stage && comparison.Area != GitAreaWorking && comparison.Area != GitAreaUntracked || !stage && comparison.Area != GitAreaStaged {
		return errors.New("git action does not match the reviewed comparison area")
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
	state, err := s.readReviewState(ctx)
	if err != nil {
		return err
	}
	defer state.close()
	lock, indexPath, err := gitReviewIndexLock(state.repo)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close(); _ = os.Remove(lock.Name()) }()
	verify := func() error {
		fresh, err := s.readReviewState(ctx)
		if err != nil {
			return err
		}
		defer fresh.close()
		current, err := fresh.compare(ctx, GitChange{Path: comparison.Path, AbsolutePath: comparison.AbsolutePath, Area: comparison.Area})
		if err != nil {
			return err
		}
		if current.Fingerprint != comparison.Fingerprint || gitComparisonIdentity(current) != gitComparisonIdentity(comparison) {
			return ErrGitReviewStale
		}
		return nil
	}
	if err := verify(); err != nil {
		return err
	}
	idx := state.idx
	entries := make([]*index.Entry, 0, len(idx.Entries)+1)
	for _, entry := range idx.Entries {
		if entry.Name != comparison.Path {
			entries = append(entries, entry)
		}
	}
	text, mode, present, err := target()
	if err != nil {
		return err
	}
	if present {
		if mode == filemode.Submodule {
			return errors.New("submodule staging requires a dedicated submodule operation")
		}
		// Staging carries working-tree text, which takes the same clean
		// conversion `git add` applies; unstaging restores repository bytes.
		hash, err := gitWriteBlob(ctx, state.root, comparison.Path, text, stage && mode != filemode.Symlink)
		if err != nil {
			return err
		}
		// No stat data, exactly like `git reset`/read-tree entries: Git then
		// compares content through its clean filter instead of trusting a
		// size that describes the blob, not the checked-out file.
		entries = append(entries, &index.Entry{Name: comparison.Path, Hash: hash, Mode: mode})
	}
	idx.Entries, idx.Cache, idx.ResolveUndo, idx.EndOfIndexEntry = entries, nil, nil, nil
	if err := index.NewEncoder(lock).Encode(idx); err != nil {
		return err
	}
	if err := lock.Sync(); err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	if err := verify(); err != nil {
		return err
	}
	return fileio.CommitAtomicFile(lock.Name(), indexPath)
}

func gitReviewIndexLock(repo *git.Repository) (*os.File, string, error) {
	storage, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return nil, "", errors.New("reviewed Git mutations require a filesystem repository")
	}
	indexPath := filepath.Join(storage.Filesystem().Root(), "index")
	lock, err := os.OpenFile(indexPath+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("lock Git index (another Git operation may be active): %w", err)
	}
	return lock, indexPath, nil
}

func (s *GitService) ReviewCommit(ctx context.Context) (GitCommitReview, error) {
	if s == nil {
		return GitCommitReview{}, errors.New("git service is unavailable")
	}
	if err := s.lock(ctx); err != nil {
		return GitCommitReview{}, err
	}
	defer s.unlock()
	state, err := s.readReviewState(ctx)
	if err != nil {
		return GitCommitReview{}, err
	}
	defer state.close()
	return state.commitReview(ctx)
}

func (state *gitReviewState) commitReview(ctx context.Context) (GitCommitReview, error) {
	review := GitCommitReview{RepositoryRoot: state.root, Fingerprint: state.fingerprint}
	if state.head != nil {
		review.Head = state.head.Hash().String()
	}
	paths := make(map[string]bool)
	indexed := make(map[string]*index.Entry, len(state.idx.Entries))
	for _, entry := range state.idx.Entries {
		if entry.Stage != 0 {
			return GitCommitReview{}, errors.New("resolve index conflicts before reviewing a commit")
		}
		if !entry.IntentToAdd {
			paths[entry.Name] = true
			indexed[entry.Name] = entry
		}
	}
	if state.headTree != nil {
		walker := object.NewTreeWalker(state.headTree, true, nil)
		defer walker.Close()
		for {
			name, entry, err := walker.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return GitCommitReview{}, err
			}
			if entry.Mode != filemode.Dir {
				paths[name] = true
			}
		}
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	var reviewedBytes int64
	for _, name := range names {
		if state.headTree != nil && indexed[name] != nil {
			entry, err := state.headTree.FindEntry(name)
			if err == nil && entry.Hash == indexed[name].Hash && entry.Mode == indexed[name].Mode {
				continue
			}
		}
		before, err := state.treeFile(ctx, state.headTree, name)
		if err != nil {
			return GitCommitReview{}, err
		}
		after, err := state.indexFile(ctx, name)
		if err != nil {
			return GitCommitReview{}, err
		}
		if before == after {
			continue
		}
		reviewedBytes += int64(len(before.text) + len(after.text))
		if reviewedBytes > gitComparisonLimit {
			return GitCommitReview{}, errors.New("staged review exceeds the 64 MiB review limit")
		}
		comparison := gitReviewComparison(state.root, name, GitAreaStaged, before, after, "HEAD", "Index (staged)")
		comparison.Fingerprint = gitReviewDigest(state.fingerprint, gitComparisonIdentity(comparison))
		review.Comparisons = append(review.Comparisons, comparison)
	}
	return review, ctx.Err()
}

func (s *GitService) InspectCommit(ctx context.Context, hash string) (GitCommitEvidence, error) {
	if s == nil {
		return GitCommitEvidence{}, errors.New("git service is unavailable")
	}
	if len(hash) != 40 {
		return GitCommitEvidence{}, errors.New("commit inspection requires a complete commit hash")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return GitCommitEvidence{}, errors.New("invalid commit hash")
	}
	if err := s.lock(ctx); err != nil {
		return GitCommitEvidence{}, err
	}
	defer s.unlock()
	state, err := s.readReviewState(ctx)
	if err != nil {
		return GitCommitEvidence{}, err
	}
	defer state.close()
	commit, err := state.repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return GitCommitEvidence{}, err
	}
	target, err := commit.Tree()
	if err != nil {
		return GitCommitEvidence{}, err
	}
	var base *object.Tree
	baseLabel := "Empty tree"
	if len(commit.ParentHashes) > 0 {
		parent, err := commit.Parent(0)
		if err != nil {
			return GitCommitEvidence{}, err
		}
		base, err = parent.Tree()
		if err != nil {
			return GitCommitEvidence{}, err
		}
		baseLabel = "Parent " + shortGitHash(parent.Hash.String())
	}
	changes, err := base.DiffContext(ctx, target)
	if err != nil {
		return GitCommitEvidence{}, err
	}
	evidence := GitCommitEvidence{
		Commit:  GitCommitSummary{Hash: hash, ShortHash: shortGitHash(hash), Subject: strings.SplitN(commit.Message, "\n", 2)[0], Author: commit.Author.Name, When: commit.Author.When},
		Message: commit.Message,
	}
	var total int64
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return GitCommitEvidence{}, err
		}
		name := change.To.Name
		if name == "" {
			name = change.From.Name
		}
		before, after := gitReviewFile{}, gitReviewFile{}
		if change.From.Name != "" {
			before, err = state.treeFile(ctx, base, change.From.Name)
		}
		if err == nil && change.To.Name != "" {
			after, err = state.treeFile(ctx, target, change.To.Name)
		}
		if err != nil {
			return GitCommitEvidence{}, err
		}
		total += int64(len(before.text) + len(after.text))
		if total > gitComparisonLimit {
			return GitCommitEvidence{}, errors.New("commit review exceeds the 64 MiB review limit")
		}
		comparison := gitReviewComparison(state.root, name, GitAreaStaged, before, after, baseLabel, "Commit "+shortGitHash(hash))
		comparison.Historical = true
		if change.From.Name != "" && change.From.Name != name {
			comparison.OriginalPath = change.From.Name
		}
		comparison.Fingerprint = gitReviewDigest(hash, gitComparisonIdentity(comparison))
		evidence.Comparisons = append(evidence.Comparisons, comparison)
	}
	return evidence, ctx.Err()
}
