package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	captypes "crude/types/capability"
)

type skillDirectoryResult struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	SkillID  string `json:"skillId"`
	Name     string `json:"name"`
	Installs int64  `json:"installs"`
}

func discoverSkillDirectory(ctx context.Context, source captypes.SourceConfig, query string, limit int) ([]Candidate, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	endpoint, err := url.Parse(source.Location)
	if err != nil {
		return nil, err
	}
	values := endpoint.Query()
	values.Set("q", query)
	values.Set("limit", strconv.Itoa(min(100, max(20, limit))))
	endpoint.RawQuery = values.Encode()
	data, err := boundedFetch(ctx, endpoint.String(), 4<<20)
	if err != nil {
		return nil, err
	}
	var response struct {
		Skills []skillDirectoryResult `json:"skills"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, min(limit, len(response.Skills)))
	for _, result := range response.Skills {
		owner, repository, ok := githubSlug(result.Source)
		if !ok || result.SkillID == "" || result.ID == "" {
			continue
		}
		name := result.Name
		if name == "" {
			name = result.SkillID
		}
		repositoryURL := "https://github.com/" + owner + "/" + repository
		entry := captypes.Entry{
			ID: source.ID + ":" + result.ID, Kind: captypes.KindSkill, Name: name,
			Description: "Community skill from " + result.Source,
			Source:      source.ID, Publisher: result.Source, Origin: source.Scope,
			State: "not acquired", Managed: true, Repository: repositoryURL,
			Website: "https://skills.sh/" + result.ID, Popularity: result.Installs,
		}
		candidates = append(candidates, Candidate{
			Entry: entry, Repository: repositoryURL, SkillID: result.SkillID,
			Requirements: []string{"Repository HEAD is resolved to an exact commit during install review"},
		})
		if len(candidates) >= limit {
			break
		}
	}
	return candidates, nil
}

func githubSlug(value string) (string, string, bool) {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		strings.ContainsAny(parts[0]+parts[1], "?#\\") {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func resolveSkillDirectoryCandidate(ctx context.Context, candidate Candidate) (Candidate, error) {
	if candidate.Repository == "" || candidate.SkillID == "" || candidate.ArchiveURL != "" {
		return candidate, nil
	}
	location := strings.TrimSuffix(strings.TrimPrefix(candidate.Repository, "https://github.com/"), ".git")
	owner, repository, ok := githubSlug(location)
	if !ok {
		return candidate, errors.New("skill directory result is not a supported GitHub repository")
	}
	commitData, err := boundedFetch(ctx, "https://api.github.com/repos/"+owner+"/"+repository+"/commits/HEAD", 2<<20)
	if err != nil {
		return candidate, err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(commitData, &commit); err != nil {
		return candidate, err
	}
	if len(commit.SHA) != 40 {
		return candidate, errors.New("skill repository did not return an exact commit identity")
	}
	treeData, err := boundedFetch(ctx, "https://api.github.com/repos/"+owner+"/"+repository+"/git/trees/"+commit.SHA+"?recursive=1", 8<<20)
	if err != nil {
		return candidate, err
	}
	var tree struct {
		Truncated bool `json:"truncated"`
		Entries   []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(treeData, &tree); err != nil {
		return candidate, err
	}
	if tree.Truncated {
		return candidate, errors.New("skill repository tree is incomplete; add a smaller pinned repository source")
	}
	subpath, err := findSkillSubpath(tree.Entries, candidate.SkillID)
	if err != nil {
		return candidate, err
	}
	candidate.Entry.Version = commit.SHA
	candidate.Entry.Path = subpath
	candidate.ArchiveURL = "https://codeload.github.com/" + owner + "/" + repository + "/tar.gz/" + commit.SHA
	candidate.Subpath = subpath
	return candidate, nil
}

func findSkillSubpath(entries []struct {
	Path string `json:"path"`
	Type string `json:"type"`
}, skillID string) (string, error) {
	var matches []string
	for _, entry := range entries {
		if entry.Type != "blob" || path.Base(entry.Path) != "SKILL.md" {
			continue
		}
		directory := path.Dir(entry.Path)
		if directory == "." {
			directory = ""
		}
		if directory == "" || strings.EqualFold(path.Base(directory), skillID) {
			matches = append(matches, directory)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("skill %q is no longer present in the repository; refresh Discover", skillID)
	}
	sort.Slice(matches, func(first, second int) bool {
		firstDepth, secondDepth := strings.Count(matches[first], "/"), strings.Count(matches[second], "/")
		if firstDepth == secondDepth {
			return matches[first] < matches[second]
		}
		return firstDepth < secondDepth
	})
	return matches[0], nil
}
