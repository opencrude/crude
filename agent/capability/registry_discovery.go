package capability

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	captypes "crude/types/capability"
)

type registryInput struct {
	Type        string                   `json:"type"`
	Name        string                   `json:"name"`
	Value       string                   `json:"value"`
	Default     string                   `json:"default"`
	Description string                   `json:"description"`
	Required    bool                     `json:"isRequired"`
	Secret      bool                     `json:"isSecret"`
	Variables   map[string]registryInput `json:"variables"`
}

type registryTransport struct {
	Type      string                   `json:"type"`
	URL       string                   `json:"url"`
	Headers   []registryInput          `json:"headers"`
	Variables map[string]registryInput `json:"variables"`
}

//go:embed registry_launch.json
var registryLaunchData []byte

// Version-specific publisher instructions fill missing registry metadata only.
// A corrected upstream record always wins. These are data, never CLI guesses.
type launchSupplement struct {
	Source, Server, RegistryType, Package, Version, Reference string
	PackageArguments                                          []registryInput
}

var launchSupplements = func() []launchSupplement {
	var rows []launchSupplement
	if err := json.Unmarshal(registryLaunchData, &rows); err != nil {
		panic(err)
	}
	return rows
}()

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

// registrySearchTerms turns a keyword query into the registry's own terms.
// The registry matches one substring of a server name, so a sentence matches
// nothing: each keyword is asked separately, and adjacent keywords are also
// joined the way names join them ("computer use" also asks "computer-use").
func registrySearchTerms(query string) []string {
	words := newCapabilityQuery(query).terms
	var terms []string
	seen := make(map[string]bool)
	add := func(term string) {
		if len(term) >= 3 && !seen[term] && len(terms) < maxRegistrySearches {
			seen[term] = true
			terms = append(terms, term)
		}
	}
	for i := 0; i+1 < len(words); i++ {
		add(words[i] + "-" + words[i+1])
	}
	for _, word := range words {
		add(word)
	}
	if len(terms) == 0 {
		terms = []string{strings.TrimSpace(query)}
	}
	return terms
}

// maxRegistrySearches bounds the requests one search makes to a registry.
const maxRegistrySearches = 8

// discoverMCPRegistry asks the registry for every term of the query, then
// ranks the union locally by name and description across all query words.
func discoverMCPRegistry(ctx context.Context, source captypes.SourceConfig, query string, limit int) ([]Candidate, error) {
	terms := registrySearchTerms(query)
	results := make([][]Candidate, len(terms))
	errs := make([]error, len(terms))
	var wg sync.WaitGroup
	for i, term := range terms {
		wg.Go(func() { results[i], errs[i] = discoverMCPRegistryTerm(ctx, source, term, limit) })
	}
	wg.Wait()
	positions := make(map[string]int)
	var candidates []Candidate
	for _, found := range results {
		for _, candidate := range found {
			if _, exists := positions[candidate.Entry.ID]; !exists {
				positions[candidate.Entry.ID] = len(candidates)
				candidates = append(candidates, candidate)
			}
		}
	}
	matcher := newCapabilityQuery(query)
	sort.SliceStable(candidates, func(i, j int) bool {
		if (candidates[i].Entry.State == "unsupported") != (candidates[j].Entry.State == "unsupported") {
			return candidates[j].Entry.State == "unsupported"
		}
		return matcher.score(candidates[i].Entry) > matcher.score(candidates[j].Entry)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	// A failed term is reported only when nothing answered the query.
	if len(candidates) == 0 {
		return nil, errors.Join(errs...)
	}
	return candidates, ctx.Err()
}

func discoverMCPRegistryTerm(ctx context.Context, source captypes.SourceConfig, query string, limit int) ([]Candidate, error) {
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
		values.Set("version", "latest")
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
		transport := "http"
		if remote.Type == "sse" {
			transport = captypes.TransportSSE
		}
		if remote.Type != "streamable-http" && remote.Type != "sse" {
			entry := baseEntry
			entry.ID = registryConnectionID(source.ID, server.Name, "remote:"+remote.Type, remote.URL)
			entry.Transport, entry.State = "remote", "unsupported"
			entry.Reason = "unsupported registry remote transport " + fmt.Sprintf("%q", remote.Type)
			candidates = append(candidates, Candidate{Entry: entry})
			continue
		}
		id := registryConnectionID(source.ID, server.Name, "remote", remote.URL)
		config := captypes.ConnectionConfig{
			ID: id, Name: name, Transport: transport, URL: remote.URL,
			Source: source.ID, Version: server.Version, Scope: source.Scope,
			RepositoryURL: server.Repository.URL, WebsiteURL: server.WebsiteURL,
		}
		entry := baseEntry
		entry.ID, entry.Transport, entry.ConnectionID = id, "remote", id
		endpoint, err := registryValue(registryInput{Value: remote.URL, Variables: remote.Variables})
		if err != nil {
			entry.State, entry.Reason = "unsupported", "Endpoint needs manual configuration in Settings: "+err.Error()
			candidates = append(candidates, Candidate{Entry: entry})
			continue
		}
		config.URL = endpoint
		var requirements []string
		for _, header := range remote.Headers {
			value, valueErr := registryValue(header)
			if value == "" && !header.Required && valueErr == nil {
				continue
			}
			if config.Headers == nil {
				config.Headers = make(map[string]string)
			}
			// Empty required headers remain editable on the existing connection;
			// they block connection, not installation, and never invent credentials.
			config.Headers[header.Name] = value
			if value == "" || valueErr != nil {
				config.Headers[header.Name] = ""
				entry.State, entry.Reason = "setup required", "Configure required header "+header.Name+" in Settings (use Secret references for credentials)"
			}
			requirements = append(requirements, "Header: "+header.Name+" — "+header.Description)
		}
		candidates = append(candidates, Candidate{Entry: entry, Connection: &config, Requirements: requirements})
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
	if packageSpec.RuntimeHint != "" && packageSpec.RuntimeHint != command {
		return captypes.ConnectionConfig{}, nil, fmt.Errorf("package requests %s; use its published launch command to add the connection", packageSpec.RuntimeHint)
	}
	args = append(args, runtimeArgs...)
	if packageSpec.RegistryType == "npm" {
		args = append(args, packageSpec.Identifier+"@"+version)
	} else {
		args = append(args, packageSpec.Identifier+"=="+version)
	}
	reference := ""
	if packageSpec.PackageArguments == nil {
		for _, row := range launchSupplements {
			if row.Source == source.Location && row.Server == server.Name && row.RegistryType == packageSpec.RegistryType && row.Package == packageSpec.Identifier && row.Version == packageSpec.Version {
				packageSpec.PackageArguments = row.PackageArguments
				reference = row.Reference
				break
			}
		}
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
	if reference != "" {
		requirements = append(requirements, "Launch arguments from publisher: "+reference)
	}
	for _, input := range packageSpec.EnvironmentVariables {
		if input.Name == "" {
			continue
		}
		value, valueErr := registryValue(input)
		if valueErr != nil {
			value = ""
		}
		if value != "" {
			environment[input.Name] = value
		}
		if valueErr != nil || input.Required && value == "" {
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
		value, err := registryValue(input)
		if err != nil {
			return nil, fmt.Errorf("registry command argument needs manual configuration in Settings: %w", err)
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

// Substitute only declared variables. Braces without a matching declaration
// are literal by the registry contract, not guessed placeholders.
func registryValue(input registryInput) (string, error) {
	value := input.Value
	if value == "" {
		value = input.Default
	}
	keys := make([]string, 0, len(input.Variables))
	for key := range input.Variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var replacements []string
	for _, key := range keys {
		if !strings.Contains(value, "{"+key+"}") {
			continue
		}
		variable := input.Variables[key]
		text := variable.Value
		if text == "" {
			text = variable.Default
		}
		if text == "" {
			return "", fmt.Errorf("supply variable %q: %s", key, variable.Description)
		}
		replacements = append(replacements, "{"+key+"}", text)
	}
	return strings.NewReplacer(replacements...).Replace(value), nil
}

func registryConnectionID(sourceID, serverName, variant, identity string) string {
	digest := sha256.Sum256([]byte(sourceID + "\n" + serverName + "\n" + variant + "\n" + identity))
	return "registry_" + hex.EncodeToString(digest[:])[:20]
}
