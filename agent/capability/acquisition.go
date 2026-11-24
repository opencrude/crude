package capability

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crude/types/command"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"crude/agent/mcpclient"
	"crude/agent/skills"
	"crude/internal/network"
	captypes "crude/types/capability"
)

type Candidate struct {
	Entry        captypes.Entry
	ArchiveURL   string
	Subpath      string
	Connection   *captypes.ConnectionConfig
	Repository   string
	SkillID      string
	Requirements []string
}

// Shared immutable downloads are keyed by artifact, not by activation. A
// detached requester leaves the cache fill intact. Stopping the last owning
// job cancels and joins it; it must never interrupt a different waiter.
type acquisition struct {
	done         chan struct{}
	cancel       context.CancelFunc
	path, digest string
	err          error
	waiters      map[*acquisitionWaiter]struct{}
	progress     command.Progress
}
type acquisitionWaiter struct{ report func(command.Progress) }

var acquisitions = struct {
	sync.Mutex
	jobs map[string]*acquisition
}{jobs: make(map[string]*acquisition)}

func (job *acquisition) report(p command.Progress) {
	acquisitions.Lock()
	defer acquisitions.Unlock()
	job.progress = p
	for waiter := range job.waiters {
		if waiter.report != nil {
			waiter.report(p)
		}
	}
}

func boundedFetch(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	var data bytes.Buffer
	err := fetchSource(ctx, endpoint, "application/json", 45*time.Second, limit, &data)
	return data.Bytes(), err
}

// fetchSource streams bytes into the caller's destination. Discovery bounds its
// in-memory documents. Artifact size and total time are user choices instead.
func fetchSource(ctx context.Context, endpoint, accept string, timeout time.Duration, limit int64, dst io.Writer) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Scheme != "https" && (parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		return errors.New("capability source needs HTTPS or a loopback test endpoint")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", accept)
	client := network.NewClient(timeout)
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= 5 || request.URL.Scheme != "https" && request.URL.Host != parsed.Host {
			return errors.New("unsafe source redirect")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("capability source returned HTTP %d", response.StatusCode)
	}
	var body io.Reader = response.Body
	if sink := captypes.ProgressObserver(ctx); sink != nil && response.ContentLength > 0 {
		body = &transferReader{Reader: body, ctx: ctx, total: response.ContentLength}
	}
	if limit > 0 && response.ContentLength > limit {
		return fmt.Errorf("download exceeds the %d-byte limit", limit)
	}
	if limit > 0 {
		body = io.LimitReader(body, limit+1)
	}
	n, err := io.Copy(dst, body)
	if err == nil && limit > 0 && n > limit {
		return fmt.Errorf("download exceeds the %d-byte limit", limit)
	}
	return err
}

func repositoryIdentity(source captypes.SourceConfig) (string, string, string, error) {
	location := strings.TrimSuffix(strings.TrimPrefix(source.Location, "https://github.com/"), ".git")
	parts := strings.Split(strings.Trim(location, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(parts[0]+parts[1], "?#\\") {
		return "", "", "", errors.New("repository sources use a GitHub owner/repository URL")
	}
	revision := source.Revision
	if len(revision) != 40 {
		return "", "", "", errors.New("repository source must pin a complete 40-character commit revision")
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return "", "", "", errors.New("repository revision must be a commit digest, not a branch")
	}
	return parts[0], parts[1], revision, nil
}

func (s *Service) discoverRemote(ctx context.Context, source captypes.SourceConfig, query string, kind captypes.Kind, limit int) ([]captypes.Entry, error) {
	if limit <= 0 {
		return nil, nil
	}
	var candidates []Candidate
	switch source.Kind {
	case "repository":
		owner, repository, revision, err := repositoryIdentity(source)
		if err != nil {
			return nil, err
		}
		data, err := boundedFetch(ctx, "https://api.github.com/repos/"+owner+"/"+repository+"/git/trees/"+revision+"?recursive=1", 8<<20)
		if err != nil {
			return nil, err
		}
		var tree struct {
			Truncated bool `json:"truncated"`
			Entries   []struct {
				Path string `json:"path"`
				Type string `json:"type"`
			} `json:"tree"`
		}
		if err := json.Unmarshal(data, &tree); err != nil {
			return nil, err
		}
		if tree.Truncated {
			return nil, errors.New("repository tree is incomplete; select a smaller source")
		}
		var entries []captypes.Entry
		byID := make(map[string]Candidate)
		for _, file := range tree.Entries {
			if file.Type != "blob" || !strings.HasSuffix(file.Path, "/SKILL.md") || kind == captypes.KindMCP {
				continue
			}
			name := path.Base(path.Dir(file.Path))
			entry := captypes.Entry{ID: source.ID + ":" + path.Dir(file.Path), Kind: captypes.KindSkill, Name: name, Source: source.ID, Publisher: owner, Version: revision, Origin: source.Scope, Managed: true, State: "not acquired"}
			entries = append(entries, entry)
			byID[entry.ID] = Candidate{Entry: entry, ArchiveURL: "https://codeload.github.com/" + owner + "/" + repository + "/tar.gz/" + revision, Subpath: path.Dir(file.Path)}
		}
		for _, entry := range rankCapabilityEntries(entries, newCapabilityQuery(query), limit) {
			candidates = append(candidates, byID[entry.ID])
		}
	case "registry":
		if kind == captypes.KindSkill {
			break
		}
		var err error
		candidates, err = discoverMCPRegistry(ctx, source, query, limit)
		if err != nil {
			return s.cacheDiscoveredCandidates(source, kind, candidates, limit), err
		}
	case "skill-registry":
		if kind == captypes.KindMCP {
			break
		}
		var err error
		candidates, err = discoverSkillDirectory(ctx, source, query, limit)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported capability source kind %q", source.Kind)
	}
	return s.cacheDiscoveredCandidates(source, kind, candidates, limit), nil
}

func (s *Service) cacheDiscoveredCandidates(source captypes.SourceConfig, kind captypes.Kind, candidates []Candidate, limit int) []captypes.Entry {
	entries := make([]captypes.Entry, 0, min(limit, len(candidates)))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.candidates == nil {
		s.candidates = make(map[string]Candidate)
	}
	for _, candidate := range candidates {
		if len(entries) >= limit {
			break
		}
		candidate.Entry.Source = source.ID
		if candidate.Entry.ID == "" || kind != "" && candidate.Entry.Kind != kind {
			continue
		}
		s.candidates[candidate.Entry.ID] = candidate
		entries = append(entries, candidate.Entry)
	}
	return entries
}

// Acquire installs a candidate as an MCP connection or a managed skill artifact.
func (s *Service) Acquire(ctx context.Context, id string, scope captypes.Scope) (captypes.Settings, error) {
	s.mu.RLock()
	candidate, exists := s.candidates[id]
	settings := captypes.Clone(s.settings)
	s.mu.RUnlock()
	if !exists {
		return settings, errors.New("candidate is unavailable; run discovery and inspect the exact revision")
	}
	if candidate.Entry.State == "unsupported" {
		return settings, fmt.Errorf("unsupported catalog entry: %s", candidate.Entry.Reason)
	}
	source, trusted := settings.Sources[candidate.Entry.Source]
	if !trusted || !source.Trusted || source.Disabled {
		return settings, errors.New("candidate's configured source is unavailable")
	}
	if candidate.Connection != nil {
		connection := *candidate.Connection
		connection.ID = id
		connection.Scope = scope
		connection.Source = source.ID
		if err := mcpclient.Validate(connection); err != nil {
			return settings, err
		}
		settings.Connections[connection.ID] = connection
		return settings, nil
	}
	path, digest, err := s.acquireArtifact(ctx, candidate, settings.Acquisition)
	if err != nil {
		return settings, err
	}
	skill, err := skills.LoadContext(ctx, path)
	if err != nil {
		return settings, err
	}
	settings.Skills[id] = captypes.SkillConfig{ID: id, Name: skill.Name, Path: path, Source: source.ID, Version: candidate.Entry.Version, Digest: digest, Mode: "automatic", Managed: true, Scope: scope}
	return settings, nil
}

func (s *Service) acquireArtifact(ctx context.Context, candidate Candidate, settings captypes.AcquisitionSettings) (string, string, error) {
	keyDigest := sha256.Sum256([]byte(s.cache + "\n" + candidate.Entry.ID + "\n" + candidate.Entry.Version + "\n" + candidate.Entry.Digest + "\n" + candidate.ArchiveURL))
	key := hex.EncodeToString(keyDigest[:])
	waiter := &acquisitionWaiter{report: captypes.ProgressObserver(ctx)}
	acquisitions.Lock()
	job := acquisitions.jobs[key]
	if job == nil {
		var bounded context.Context
		var cancel context.CancelFunc
		if settings.TimeoutSeconds > 0 {
			bounded, cancel = context.WithTimeout(context.Background(), time.Duration(settings.TimeoutSeconds)*time.Second)
		} else {
			bounded, cancel = context.WithCancel(context.Background())
		}
		job = &acquisition{done: make(chan struct{}), cancel: cancel, waiters: make(map[*acquisitionWaiter]struct{})}
		acquisitions.jobs[key] = job
		go func() {
			defer cancel()
			job.path, job.digest, job.err = s.installArtifact(captypes.WithProgress(bounded, job.report), key, candidate, settings.MaxDownloadBytes)
			acquisitions.Lock()
			if acquisitions.jobs[key] == job {
				delete(acquisitions.jobs, key)
			}
			close(job.done)
			acquisitions.Unlock()
		}()
	}
	job.waiters[waiter] = struct{}{}
	if waiter.report != nil && job.progress.Phase != "" {
		waiter.report(job.progress)
	}
	acquisitions.Unlock()
	select {
	case <-ctx.Done():
		acquisitions.Lock()
		delete(job.waiters, waiter)
		stop := len(job.waiters) == 0 && errors.Is(context.Cause(ctx), command.ErrStopped) && acquisitions.jobs[key] == job
		if stop {
			delete(acquisitions.jobs, key)
			job.cancel()
		}
		acquisitions.Unlock()
		if stop {
			<-job.done
		}
		return "", "", ctx.Err()
	case <-job.done:
		return job.path, job.digest, job.err
	}

}

func (s *Service) installArtifact(ctx context.Context, key string, candidate Candidate, limit int64) (string, string, error) {
	if err := os.MkdirAll(s.cache, 0700); err != nil {
		return "", "", err
	}
	lock := flock.New(filepath.Join(s.cache, key+".lock"))
	locked, err := lock.TryLockContext(ctx, 30*time.Millisecond)
	if err != nil || !locked {
		return "", "", fmt.Errorf("artifact lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	directoryName := candidate.Entry.Name
	if candidate.SkillID != "" {
		directoryName = candidate.SkillID
	}
	destination := filepath.Join(s.cache, key, directoryName)
	if directoryName == "" || !filepath.IsLocal(directoryName) || strings.ContainsAny(directoryName, "/\\") {
		return "", "", errors.New("artifact name is not a contained directory identity")
	}
	if skill, err := skills.LoadContext(ctx, destination); err == nil {
		if candidate.ArchiveURL == "" && skill.Digest != candidate.Entry.Digest {
			return "", "", errors.New("cached skill differs from the pinned source digest")
		}
		return destination, skill.Digest, nil
	}
	if candidate.ArchiveURL == "" {
		source, err := skills.LoadContext(ctx, candidate.Entry.Path)
		if err != nil {
			return "", "", err
		}
		if source.Digest != candidate.Entry.Digest {
			return "", "", errors.New("local source changed since discovery; inspect it again")
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return "", "", err
		}
		copied, err := skills.MakeLocalCopy(ctx, source, destination)
		if err != nil {
			return "", "", err
		}
		return destination, copied.Digest, nil
	}
	staging, err := os.MkdirTemp(s.cache, ".acquire-")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	// Keep untrusted archive bytes off the heap. Verify the complete transfer,
	// then extract the selected subtree. Nothing is published on any failure.
	archive, err := os.CreateTemp(staging, ".download-")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = archive.Close() }()
	captypes.ReportProgress(ctx, command.PhaseDownloading, 0, 0)
	var sink io.Writer = archive
	hasher := sha256.New()
	if candidate.Entry.Digest != "" {
		sink = io.MultiWriter(archive, hasher)
	}
	if err = fetchSource(ctx, candidate.ArchiveURL, "application/octet-stream", 0, limit, sink); err != nil {
		return "", "", err
	}
	captypes.ReportProgress(ctx, "Verifying skill files", 0, 0)
	if candidate.Entry.Digest != "" && hex.EncodeToString(hasher.Sum(nil)) != candidate.Entry.Digest {
		return "", "", errors.New("download integrity differs from the inspected digest")
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	root := filepath.Join(staging, directoryName)
	if err := extractSkill(ctx, archive, candidate.Subpath, root, limit); err != nil {
		return "", "", err
	}
	if err = archive.Close(); err != nil {
		return "", "", err
	}
	if err = os.Remove(archive.Name()); err != nil {
		return "", "", err
	}
	skill, err := skills.LoadContext(ctx, root)
	if err != nil {
		return "", "", err
	}
	if candidate.SkillID != "" && !strings.EqualFold(skill.Name, candidate.SkillID) &&
		!strings.EqualFold(skill.Name, candidate.Entry.Name) {
		return "", "", errors.New("acquired skill identity differs from the reviewed directory result")
	}
	if err := os.Rename(staging, filepath.Join(s.cache, key)); err != nil {
		return "", "", err
	}
	return destination, skill.Digest, nil
}

func extractSkill(ctx context.Context, data io.Reader, subpath, root string, limit int64) error {
	if subpath != "" && (!filepath.IsLocal(subpath) || strings.Contains(subpath, "\\") || path.Clean(subpath) != subpath) {
		return errors.New("artifact subtree is not a contained path")
	}
	gzipReader, err := gzip.NewReader(data)
	if err != nil {
		return err
	}
	defer func() { _ = gzipReader.Close() }()
	archive := tar.NewReader(gzipReader)
	bytesWritten := int64(0)
	files := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(header.Name) || strings.Contains(header.Name, "\\") || path.Clean(header.Name) != strings.TrimSuffix(header.Name, "/") {
			return errors.New("artifact contains an unsafe path")
		}
		name := strings.TrimPrefix(header.Name, strings.SplitN(header.Name, "/", 2)[0]+"/")
		prefix := strings.Trim(subpath, "/")
		if prefix != "" {
			prefix += "/"
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		relative := strings.TrimPrefix(name, prefix)
		if relative == "" {
			continue
		}
		if !filepath.IsLocal(relative) || strings.Contains(relative, "\\") || header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return errors.New("artifact contains an unsafe path or unsupported link")
		}
		bytesWritten += header.Size
		files++
		if header.Size < 0 || limit > 0 && bytesWritten > limit || files > 4096 {
			return errors.New("expanded artifact exceeded its file or byte budget")
		}
		target := filepath.Join(root, filepath.FromSlash(relative))
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(output, archive, header.Size)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
}

// transferReader reports whole-percent changes, never a UI event per packet.
type transferReader struct {
	io.Reader
	ctx                  context.Context
	total, read, percent int64
}

func (r *transferReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += int64(n)
	pct := 100 * r.read / r.total
	if pct != r.percent {
		r.percent = pct
		captypes.ReportProgress(r.ctx, command.PhaseDownloading, r.read, r.total)
	}
	return n, err
}
