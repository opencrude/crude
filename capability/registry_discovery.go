package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	captypes "crude/types/capability"
)

type registryInput struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Value       string `json:"value"`
	Default     string `json:"default"`
	Description string `json:"description"`
	Required    bool   `json:"isRequired"`
	Secret      bool   `json:"isSecret"`
}

type registryTransport struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type registryPackage struct {
	RegistryType         string            `json:"registryType"`
	Identifier           string            `json:"identifier"`
	Version              string            `json:"version"`
	RuntimeHint          string            `json:"runtimeHint"`
	RuntimeArguments     []registryInput   `json:"runtimeArguments"`
	PackageArguments     []registryInput   `json:"packageArguments"`
	EnvironmentVariables []registryInput   `json:"environmentVariables"`
	Transport            registryTransport `json:"transport"`
}

type registryServer struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
	WebsiteURL  string `json:"websiteUrl"`
	Repository  struct {
		URL string `json:"url"`
	} `json:"repository"`
	Remotes  []registryTransport `json:"remotes"`
	Packages []registryPackage   `json:"packages"`
}

type registryEnvelope struct {
	Server registryServer `json:"server"`
	Meta   struct {
		Official struct {
			Status   string `json:"status"`
			IsLatest *bool  `json:"isLatest"`
		} `json:"io.modelcontextprotocol.registry/official"`
	} `json:"_meta"`
}

type registryCatalog struct {
	Servers  []registryEnvelope `json:"servers"`
	Entries  []Candidate        `json:"entries"`
	Metadata struct {
		NextCursor string `json:"nextCursor"`
	} `json:"metadata"`
}

func discoverMCPRegistry(ctx context.Context, source captypes.SourceConfig, query string, limit int) ([]Candidate, error) {
	baseEndpoint, err := url.Parse(source.Location)
	if err != nil {
		return nil, err
	}
	cursor := ""
	positions := make(map[string]int)
	candidates := make([]Candidate, 0, limit)
	add := func(candidate Candidate) {
		if candidate.Entry.ID == "" {
			return
		}
		if index, exists := positions[candidate.Entry.ID]; exists {
			candidates[index] = candidate
			return
		}
		positions[candidate.Entry.ID] = len(candidates)
		candidates = append(candidates, candidate)
	}
	supportedCount := func() int {
		count := 0
		for _, candidate := range candidates {
			if candidate.Entry.State != "unsupported" {
				count++
			}
		}
		return count
	}
	seenCursors := map[string]bool{"": true}
	for page := 0; page < 8 && supportedCount() < limit; page++ {
		if err := ctx.Err(); err != nil {
			return candidates, err
		}
		endpoint := *baseEndpoint
		values := endpoint.Query()
		values.Set("search", query)
		values.Set("limit", fmt.Sprint(min(100, max(20, limit*4))))
		if cursor != "" {
			values.Set("cursor", cursor)
		}
		endpoint.RawQuery = values.Encode()
		data, fetchErr := boundedFetch(ctx, endpoint.String(), 4<<20)
		if fetchErr != nil {
			return candidates, fetchErr
		}
		var catalog registryCatalog
		if err := json.Unmarshal(data, &catalog); err != nil {
			return candidates, err
		}
		for _, envelope := range catalog.Servers {
			if envelope.Meta.Official.Status != "" && envelope.Meta.Official.Status != "active" ||
				envelope.Meta.Official.IsLatest != nil && !*envelope.Meta.Official.IsLatest {
				continue
			}
			for _, candidate := range registryServerCandidates(source, envelope.Server) {
				add(candidate)
			}
		}
		for _, candidate := range catalog.Entries {
			add(candidate)
		}
		if seenCursors[catalog.Metadata.NextCursor] {
			break
		}
		cursor = catalog.Metadata.NextCursor
		seenCursors[cursor] = true
	}
	// Preserve registry relevance within usable and unavailable groups, but do
	// not let an unsupported first page conceal a usable next-page result.
	var ranked []Candidate
	for _, candidate := range candidates {
		if candidate.Entry.State != "unsupported" {
			ranked = append(ranked, candidate)
		}
	}
	for _, candidate := range candidates {
		if candidate.Entry.State == "unsupported" {
			ranked = append(ranked, candidate)
		}
	}
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

func registryServerCandidates(source captypes.SourceConfig, server registryServer) []Candidate {
	name := server.Title
	if name == "" {
		name = server.Name
	}
	baseEntry := captypes.Entry{
		Kind:        captypes.KindMCP,
		Name:        name,
		Description: server.Description,
		Source:      source.ID,
		Publisher:   server.Name,
		Version:     server.Version,
		Origin:      source.Scope,
		State:       "not acquired",
		Repository:  server.Repository.URL,
		Website:     server.WebsiteURL,
	}
	var candidates []Candidate
	for _, remote := range server.Remotes {
		if remote.Type != "streamable-http" {
			entry := baseEntry
			entry.ID = registryConnectionID(source.ID, server.Name, "remote:"+remote.Type, remote.URL)
			entry.Transport, entry.State = "remote", "unsupported"
			entry.Reason = "unsupported registry remote transport " + fmt.Sprintf("%q", remote.Type)
			candidates = append(candidates, Candidate{Entry: entry})
			continue
		}
		id := registryConnectionID(source.ID, server.Name, "remote", remote.URL)
		config := captypes.ConnectionConfig{
			ID: id, Name: name, Transport: "http", URL: remote.URL,
			Source: source.ID, Version: server.Version, Scope: source.Scope,
			RepositoryURL: server.Repository.URL, WebsiteURL: server.WebsiteURL,
		}
		entry := baseEntry
		entry.ID, entry.Transport, entry.ConnectionID = id, "remote", id
		candidates = append(candidates, Candidate{Entry: entry, Connection: &config})
	}
	for _, packageSpec := range server.Packages {
		config, requirements, err := registryPackageConnection(source, server, packageSpec, name)
		if err != nil {
			entry := baseEntry
			version := packageSpec.Version
			if version == "" {
				version = server.Version
			}
			entry.ID = registryConnectionID(source.ID, server.Name, packageSpec.RegistryType, packageSpec.Identifier+"@"+version)
			entry.Transport, entry.PackageType = "local package", packageSpec.RegistryType
			entry.State, entry.Reason = "unsupported", err.Error()
			candidates = append(candidates, Candidate{Entry: entry})
			continue
		}
		entry := baseEntry
		entry.ID, entry.Transport, entry.PackageType, entry.ConnectionID = config.ID, "local package", config.PackageType, config.ID
		if len(config.RequiredEnv) > 0 {
			entry.State = "setup required"
			entry.Reason = "Required environment: " + strings.Join(config.RequiredEnv, ", ")
		}
		candidates = append(candidates, Candidate{Entry: entry, Connection: &config, Requirements: requirements})
	}
	if len(candidates) == 0 {
		entry := baseEntry
		entry.ID = registryConnectionID(source.ID, server.Name, "unavailable", server.Version)
		entry.State, entry.Reason = "unsupported", "registry server has no remote endpoint or package to install"
		candidates = append(candidates, Candidate{Entry: entry})
	}
	return candidates
}

func registryPackageConnection(source captypes.SourceConfig, server registryServer, packageSpec registryPackage, name string) (captypes.ConnectionConfig, []string, error) {
	if packageSpec.Transport.Type != "stdio" || packageSpec.Identifier == "" {
		return captypes.ConnectionConfig{}, nil, errors.New("unsupported registry package transport")
	}
	version := packageSpec.Version
	if version == "" {
		version = server.Version
	}
	if version == "" || strings.EqualFold(version, "latest") {
		return captypes.ConnectionConfig{}, nil, errors.New("registry package is not pinned to an exact version")
	}
	command := ""
	var args []string
	switch packageSpec.RegistryType {
	case "npm":
		command = "npx"
		args = append(args, "-y")
	case "pypi":
		command = "uvx"
	default:
		return captypes.ConnectionConfig{}, nil, fmt.Errorf("unsupported registry package type %q", packageSpec.RegistryType)
	}
	runtimeArgs, err := fixedRegistryArguments(packageSpec.RuntimeArguments)
	if err != nil {
		return captypes.ConnectionConfig{}, nil, err
	}
	args = append(args, runtimeArgs...)
	if packageSpec.RegistryType == "npm" {
		args = append(args, packageSpec.Identifier+"@"+version)
	} else {
		args = append(args, packageSpec.Identifier+"=="+version)
	}
	packageArgs, err := fixedRegistryArguments(packageSpec.PackageArguments)
	if err != nil {
		return captypes.ConnectionConfig{}, nil, err
	}
	args = append(args, packageArgs...)
	environment := make(map[string]string)
	var required []string
	requirements := []string{
		"Package: " + packageSpec.RegistryType + ":" + packageSpec.Identifier + "@" + version,
		"Runtime: " + command,
	}
	for _, input := range packageSpec.EnvironmentVariables {
		if input.Name == "" {
			continue
		}
		value := input.Value
		if value == "" {
			value = input.Default
		}
		if value != "" {
			environment[input.Name] = value
		}
		if input.Required && value == "" {
			required = append(required, input.Name)
			detail := "Required environment: " + input.Name
			if input.Description != "" {
				detail += " — " + input.Description
			}
			requirements = append(requirements, detail)
		}
	}
	sort.Strings(required)
	id := registryConnectionID(source.ID, server.Name, packageSpec.RegistryType, packageSpec.Identifier+"@"+version)
	return captypes.ConnectionConfig{
		ID: id, Name: name, Transport: "stdio", Command: command, Args: args,
		Environment: environment, Source: source.ID, Version: version, Scope: source.Scope,
		PackageType: packageSpec.RegistryType, PackageID: packageSpec.Identifier,
		RequiredEnv: required, RepositoryURL: server.Repository.URL, WebsiteURL: server.WebsiteURL,
	}, requirements, nil
}

func fixedRegistryArguments(inputs []registryInput) ([]string, error) {
	var arguments []string
	for _, input := range inputs {
		value := input.Value
		if value == "" {
			value = input.Default
		}
		if value == "" {
			if input.Required {
				return nil, errors.New("registry package requires an interactive command argument")
			}
			continue
		}
		if input.Type == "named" {
			if input.Name == "" {
				return nil, errors.New("registry package has an unnamed command argument")
			}
			arguments = append(arguments, input.Name+"="+value)
		} else {
			arguments = append(arguments, value)
		}
	}
	return arguments, nil
}

func registryConnectionID(sourceID, serverName, variant, identity string) string {
	digest := sha256.Sum256([]byte(sourceID + "\n" + serverName + "\n" + variant + "\n" + identity))
	return "registry_" + hex.EncodeToString(digest[:])[:20]
}
