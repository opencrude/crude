package capability

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
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

	"crude/mcpclient"
	"crude/network"
	"crude/skills"
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

type acquisition struct {
	done   chan struct{}
	path   string
	digest string
	err    error
}

var acquisitions = struct {
	sync.Mutex
	jobs map[string]*acquisition
}{jobs: make(map[string]*acquisition)}

func boundedFetch(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Scheme != "https" && (parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		return nil, errors.New("capability source needs HTTPS or a loopback test endpoint")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	client := network.NewClient(45 * time.Second)
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= 5 || request.URL.Scheme != "https" && request.URL.Host != parsed.Host {
			return errors.New("unsafe source redirect")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("capability source returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("source response exceeded its download budget")
	}
	return data, nil
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

func (service *Service) discoverRemote(ctx context.Context, source captypes.SourceConfig, query string, kind captypes.Kind, limit int) ([]captypes.Entry, error) {
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
			return service.cacheDiscoveredCandidates(source, kind, candidates, limit), err
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
	return service.cacheDiscoveredCandidates(source, kind, candidates, limit), nil
}

func (service *Service) cacheDiscoveredCandidates(source captypes.SourceConfig, kind captypes.Kind, candidates []Candidate, limit int) []captypes.Entry {
	entries := make([]captypes.Entry, 0, min(limit, len(candidates)))
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.candidates == nil {
		service.candidates = make(map[string]Candidate)
	}
	for _, candidate := range candidates {
		if len(entries) >= limit {
			break
		}
		candidate.Entry.Source = source.ID
		if candidate.Entry.ID == "" || kind != "" && candidate.Entry.Kind != kind {
			continue
		}
		service.candidates[candidate.Entry.ID] = candidate
		entries = append(entries, candidate.Entry)
	}
	return entries
}

func (service *Service) Acquire(ctx context.Context, id string, scope captypes.Scope) (captypes.Settings, error) {
	service.mu.RLock()
	candidate, exists := service.candidates[id]
	settings := captypes.Clone(service.settings)
	service.mu.RUnlock()
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
	path, digest, err := service.acquireArtifact(ctx, candidate, settings.Acquisition)
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

func (service *Service) acquireArtifact(ctx context.Context, candidate Candidate, settings captypes.AcquisitionSettings) (string, string, error) {
	keyDigest := sha256.Sum256([]byte(service.cache + "\n" + candidate.Entry.ID + "\n" + candidate.Entry.Version + "\n" + candidate.Entry.Digest + "\n" + candidate.ArchiveURL))
	key := hex.EncodeToString(keyDigest[:])
	acquisitions.Lock()
	job := acquisitions.jobs[key]
	if job == nil {
		job = &acquisition{done: make(chan struct{})}
		acquisitions.jobs[key] = job
		go func() {
			bounded, cancel := context.WithTimeout(context.Background(), time.Duration(settings.TimeoutSeconds)*time.Second)
			defer cancel()
			job.path, job.digest, job.err = service.installArtifact(bounded, key, candidate, settings.MaxDownloadBytes)
			close(job.done)
			acquisitions.Lock()
			delete(acquisitions.jobs, key)
			acquisitions.Unlock()
		}()
	}
	acquisitions.Unlock()
	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	case <-job.done:
		return job.path, job.digest, job.err
	}
}

func (service *Service) installArtifact(ctx context.Context, key string, candidate Candidate, limit int64) (string, string, error) {
	if err := os.MkdirAll(service.cache, 0700); err != nil {
		return "", "", err
	}
	lock := flock.New(filepath.Join(service.cache, key+".lock"))
	locked, err := lock.TryLockContext(ctx, 30*time.Millisecond)
	if err != nil || !locked {
		return "", "", fmt.Errorf("artifact lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	directoryName := candidate.Entry.Name
	if candidate.SkillID != "" {
		directoryName = candidate.SkillID
	}
	destination := filepath.Join(service.cache, key, directoryName)
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
	data, err := boundedFetch(ctx, candidate.ArchiveURL, limit)
	if err != nil {
		return "", "", err
	}
	if candidate.Entry.Digest != "" {
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != candidate.Entry.Digest {
			return "", "", errors.New("download integrity differs from the inspected digest")
		}
	}
	staging, err := os.MkdirTemp(service.cache, ".acquire-")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	root := filepath.Join(staging, directoryName)
	if err := extractSkill(ctx, data, candidate.Subpath, root, limit); err != nil {
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
	if err := os.Rename(staging, filepath.Join(service.cache, key)); err != nil {
		return "", "", err
	}
	return destination, skill.Digest, nil
}

func extractSkill(ctx context.Context, data []byte, subpath, root string, limit int64) error {
	if subpath != "" && (!filepath.IsLocal(subpath) || strings.Contains(subpath, "\\") || path.Clean(subpath) != subpath) {
		return errors.New("artifact subtree is not a contained path")
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
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
		if header.Size < 0 || bytesWritten > limit || files > 4096 {
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
