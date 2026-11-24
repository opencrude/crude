package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"crude/agent/skills"
	captypes "crude/types/capability"
)

// PreviewAcquisition reports what an install would change and issues a review token.
func (s *Service) PreviewAcquisition(ctx context.Context, id string, scope captypes.Scope, operation string) (captypes.AcquisitionPreview, error) {
	if err := ctx.Err(); err != nil {
		return captypes.AcquisitionPreview{}, err
	}
	if operation != "install" && operation != "update" || scope != captypes.ScopeApplication && scope != captypes.ScopeWorkspace {
		return captypes.AcquisitionPreview{}, errors.New("select an install/update operation and application/workspace scope")
	}
	s.mu.RLock()
	candidate, exists := s.candidates[id]
	settings := captypes.Clone(s.settings)
	s.mu.RUnlock()
	if !exists {
		return captypes.AcquisitionPreview{}, errors.New("refresh Discover and inspect the exact source revision before installing or updating")
	}
	if candidate.Entry.State == "unsupported" {
		return captypes.AcquisitionPreview{}, fmt.Errorf("unsupported catalog entry: %s", candidate.Entry.Reason)
	}
	resolved, err := resolveSkillDirectoryCandidate(ctx, candidate)
	if err != nil {
		return captypes.AcquisitionPreview{}, err
	}
	if resolved.ArchiveURL != candidate.ArchiveURL || resolved.Entry.Version != candidate.Entry.Version || resolved.Subpath != candidate.Subpath {
		s.mu.Lock()
		if current, currentExists := s.candidates[id]; currentExists &&
			current.Repository == candidate.Repository && current.SkillID == candidate.SkillID {
			s.candidates[id] = resolved
		}
		s.mu.Unlock()
		candidate = resolved
	}
	source, exists := settings.Sources[candidate.Entry.Source]
	if !exists || !source.Trusted || source.Disabled {
		return captypes.AcquisitionPreview{}, errors.New("candidate source is disabled, untrusted or unavailable")
	}
	preview := captypes.AcquisitionPreview{ID: id, Kind: candidate.Entry.Kind, Scope: scope, Operation: operation, Candidate: candidate.Entry, Source: source}
	preview.Requirements = append(preview.Requirements, candidate.Requirements...)
	var existing any
	if skill, installed := settings.Skills[id]; installed {
		existing = skill
		preview.Existing = &captypes.Entry{ID: id, Kind: captypes.KindSkill, Name: skill.Name, Source: skill.Source, Version: skill.Version, Digest: skill.Digest, Origin: skill.Scope, Path: skill.Path, Managed: skill.Managed, Installed: true}
	}
	if connection, installed := settings.Connections[id]; installed {
		existing = connection
		preview.Existing = &captypes.Entry{ID: id, Kind: captypes.KindMCP, Name: connection.Name, Source: connection.Source, Version: connection.Version, Origin: connection.Scope, ConnectionID: id, Installed: true}
	}
	if operation == "update" && preview.Existing == nil {
		return captypes.AcquisitionPreview{}, errors.New("update requires an installed binding")
	}
	if candidate.Connection != nil {
		connection := candidate.Connection
		preview.Requirements = append(preview.Requirements, "Transport: "+connection.Transport)
		if connection.Transport == "stdio" {
			preview.Requirements = append(preview.Requirements, fmt.Sprintf("Launch: %s %q", connection.Command, connection.Args), "Local launch requires explicit approval; filesystem and network scope are not a sandbox")
		} else {
			preview.Requirements = append(preview.Requirements, "Endpoint: "+connection.URL)
		}
		preview.RequestedAccess = []string{"Install saves the connection. Each tool call uses the current permission mode.", "External MCP mutations are outside native file ownership and undo"}
	} else {
		preview.RequestedAccess = []string{"Load source instructions; scripts still use native execution permissions"}
		// Remote candidates carry a repository-relative subtree, not a path
		// on this host. Inspect local metadata only; pinned remote bytes are
		// validated by AcquireReviewed before a registration is published.
		if candidate.ArchiveURL == "" && candidate.Entry.Path != "" {
			skill, err := skills.Inspect(candidate.Entry.Path)
			if err != nil {
				return captypes.AcquisitionPreview{}, err
			}
			preview.Requirements = append(preview.Requirements, skill.Compatibility)
			for key, value := range skill.Metadata {
				preview.Requirements = append(preview.Requirements, key+": "+value)
			}
			preview.RequestedAccess = append(preview.RequestedAccess, skill.AllowedTools...)
		} else {
			preview.Requirements = append(preview.Requirements, "Declared requirements are verified after acquiring the pinned bytes, before activation")
		}
	}
	preview.Requirements = slices.DeleteFunc(preview.Requirements, func(value string) bool { return value == "" })
	sort.Strings(preview.Requirements)
	encoded, err := json.Marshal(struct {
		Preview   captypes.AcquisitionPreview
		Candidate Candidate
		Existing  any
		Budgets   captypes.AcquisitionSettings
	}{preview, candidate, existing, settings.Acquisition})
	if err != nil {
		return captypes.AcquisitionPreview{}, err
	}
	digest := sha256.Sum256(encoded)
	preview.Token = hex.EncodeToString(digest[:])
	return preview, nil
}

// AcquireReviewed applies a preview only while the candidate still matches its token.
func (s *Service) AcquireReviewed(ctx context.Context, preview captypes.AcquisitionPreview) (captypes.Settings, error) {
	verify := func() error {
		current, err := s.PreviewAcquisition(ctx, preview.ID, preview.Scope, preview.Operation)
		if err != nil {
			return err
		}
		if preview.Token == "" || current.Token != preview.Token {
			return errors.New("capability/source/binding changed after preview; review again")
		}
		return nil
	}
	if err := verify(); err != nil {
		return captypes.Settings{}, err
	}
	acquired, err := s.Acquire(ctx, preview.ID, preview.Scope)
	if err != nil {
		return captypes.Settings{}, err
	}
	if err := verify(); err != nil {
		return captypes.Settings{}, err
	}
	return acquired, nil
}
