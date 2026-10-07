package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"crude/codexio"
	rootprompt "crude/prompt"
	"crude/provider"
)

const guidanceVerificationPasses = 3

type projectGuidance struct {
	set           *rootprompt.GuidanceSet
	rendered      string
	digest        string
	basePrompt    string
	initialPrompt string
}

type guidanceOperandError struct{ message string }

func (e *guidanceOperandError) Error() string { return e.message }

func (a *Agent) loadProjectGuidance(ctx context.Context, systemPrompt string) (string, error) {
	if a.WorkDir == "" {
		return systemPrompt, nil
	}
	if a.guidance != nil {
		if systemPrompt == a.guidance.basePrompt || systemPrompt == a.guidance.initialPrompt {
			return a.guidance.basePrompt + "\n\n" + a.guidance.rendered, nil
		}
		rendered, err := a.renderProjectGuidance(ctx, a.guidance.set, systemPrompt)
		if err != nil {
			return "", err
		}
		a.guidance = &projectGuidance{set: a.guidance.set, digest: a.guidance.digest, basePrompt: systemPrompt, rendered: rendered,
			initialPrompt: systemPrompt + "\n\n" + rendered}
		return a.guidance.initialPrompt, nil
	}
	set, err := rootprompt.DiscoverGuidance(ctx, a.WorkDir, a.GuidancePaths)
	if err != nil {
		return "", err
	}
	guidance := &projectGuidance{set: set, digest: set.Digest(), basePrompt: systemPrompt}
	guidance.rendered, err = a.renderProjectGuidance(ctx, set, systemPrompt)
	if err != nil {
		return "", err
	}
	systemPrompt += "\n\n" + guidance.rendered
	guidance.initialPrompt = systemPrompt
	a.guidance = guidance
	return systemPrompt, nil
}

func (a *Agent) guidanceTokenBudget(systemPrompt string) (int, error) {
	window := 128_000
	if a.Provider != nil {
		window = a.Provider.ContextWindow(a.Model)
	}
	if window <= 0 {
		window = 128_000
	}
	available := window - provider.EstimateTokens(systemPrompt, a.Model) - a.outputTokenLimit(window, 0) - contextSafetyReserve(window)
	if available < 2048 {
		return 0, errors.New("model context has insufficient room for project guidance and a normal response")
	}
	budget := min(65_536, max(4096, window/8))
	budget = min(budget, available/2)
	if budget < 1024 {
		return 0, errors.New("model context has insufficient room for a bounded project-guidance ledger")
	}
	return budget, nil
}

func (a *Agent) renderProjectGuidance(ctx context.Context, set *rootprompt.GuidanceSet, systemPrompt string) (string, error) {
	if len(set.Documents()) == 0 {
		return set.Verbatim(), nil
	}
	budget, err := a.guidanceTokenBudget(systemPrompt)
	if err != nil {
		return "", err
	}
	verbatim := set.Verbatim()
	if provider.EstimateTokens(verbatim, a.Model) <= budget {
		return verbatim, nil
	}
	compilerProvider := a.CompactionProvider
	if compilerProvider == nil {
		compilerProvider = a.Provider
	}
	compilerIdentity := "unavailable"
	if compilerProvider != nil {
		compilerIdentity = strings.Join([]string{compilerProvider.ID, compilerProvider.AccountID, compilerProvider.ProtocolID(), compilerProvider.BaseURL, compilerProvider.Model}, "|")
	}
	cacheKey := set.Digest() + fmt.Sprintf(":v%s:target=%s:budget=%d:compiler=%s", rootprompt.GuidanceCompilerVersion, a.Model, budget, compilerIdentity)
	if a.GuidanceCacheLoad != nil {
		if cached, ok := a.GuidanceCacheLoad(cacheKey); ok {
			return cached, nil
		}
	}
	ledger, err := a.compileProjectGuidance(ctx, set, budget)
	if err != nil {
		return "", err
	}
	if a.GuidanceCacheStore != nil {
		a.GuidanceCacheStore(cacheKey, ledger)
	}
	return ledger, nil
}

type guidanceChunk struct {
	Path      string                  `json:"path"`
	Scope     string                  `json:"scope"`
	Kind      rootprompt.GuidanceKind `json:"kind"`
	SHA256    string                  `json:"sha256"`
	StartByte int                     `json:"start_byte"`
	EndByte   int                     `json:"end_byte"`
	FileBytes int                     `json:"file_bytes"`
	Content   string                  `json:"content"`
}

func (a *Agent) compileProjectGuidance(ctx context.Context, set *rootprompt.GuidanceSet, targetLedgerBudget int) (string, error) {
	compilerProvider := a.CompactionProvider
	compilerModel := ""
	maxOutput := a.CompactionMaxOutput
	if compilerProvider == nil {
		compilerProvider = a.Provider
	}
	if compilerProvider == nil {
		return "", errors.New("project guidance requires a configured provider")
	}
	compilerModel = compilerProvider.Model
	if compilerModel == "" {
		compilerModel = a.Model
	}
	window := compilerProvider.ContextWindow(compilerModel)
	if window <= 0 {
		window = 128_000
	}
	ledgerBudget := min(targetLedgerBudget, max(1024, window/4))
	if maxOutput <= 0 {
		maxOutput = min(maxOutputTokensForContext(window), max(4096, ledgerBudget*2), max(1024, window/3))
	} else {
		maxOutput = min(maxOutput, max(1024, window/3))
	}
	if maxOutput < ledgerBudget {
		ledgerBudget = maxOutput
	}
	if ledgerBudget < 1024 {
		return "", fmt.Errorf("guidance compiler output capacity %d cannot hold a useful complete ledger", maxOutput)
	}
	fixed := provider.EstimateTokens(rootprompt.GuidanceCompilerSystem(), compilerModel) + ledgerBudget + maxOutput + contextSafetyReserve(window) + 512
	chunkTokens := min(32_768, window-fixed)
	if chunkTokens < 1024 {
		return "", fmt.Errorf("guidance compiler context %d cannot preserve a %d-token ledger and process a complete chunk", window, ledgerBudget)
	}
	chunkBytes := chunkTokens * 3
	ledger := rootprompt.InitialGuidanceLedger()
	processed := 0
	var chunks []guidanceChunk
	for _, document := range set.Documents() {
		for start := 0; start < len(document.Content); {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			end := min(len(document.Content), start+chunkBytes)
			for end < len(document.Content) && !utf8.RuneStart(document.Content[end]) {
				end--
			}
			if end <= start {
				return "", fmt.Errorf("cannot split UTF-8 guidance %s at byte %d", document.Path, start)
			}
			chunk := guidanceChunk{Path: document.Path, Scope: document.Scope, Kind: document.Kind,
				SHA256: document.SHA256, StartByte: start, EndByte: end, FileBytes: document.Bytes, Content: document.Content[start:end]}
			chunks = append(chunks, chunk)
			payload, err := json.Marshal(chunk)
			if err != nil {
				return "", err
			}
			user := rootprompt.GuidanceCompilationRequest(ledger, payload)
			result, err := a.requestGuidanceCompilation(ctx, compilerProvider, compilerModel, maxOutput, user)
			if err != nil {
				return "", fmt.Errorf("compile complete guidance %s bytes %d-%d: %w", document.Path, start, end, err)
			}
			if strings.TrimSpace(result.Content) == "" || isTruncatedFinishReason(result.FinishReason) {
				return "", fmt.Errorf("guidance compiler returned an incomplete ledger for %s bytes %d-%d", document.Path, start, end)
			}
			ledger = strings.TrimSpace(result.Content)
			if provider.EstimateTokens(ledger, a.Model) > ledgerBudget {
				return "", fmt.Errorf("complete instruction ledger exceeds its %d-token continuation budget; no rules were silently dropped", ledgerBudget)
			}
			processed += end - start
			start = end
		}
	}
	if processed != set.TotalBytes() {
		return "", fmt.Errorf("guidance compiler processed %d of %d bytes", processed, set.TotalBytes())
	}
	verified := false
	for pass := 1; pass <= guidanceVerificationPasses; pass++ {
		changed := false
		for _, chunk := range chunks {
			payload, err := json.Marshal(chunk)
			if err != nil {
				return "", err
			}
			user := rootprompt.GuidanceVerificationRequest(ledger, payload)
			result, err := a.requestGuidanceCompilation(ctx, compilerProvider, compilerModel, maxOutput, user)
			if err != nil {
				return "", fmt.Errorf("verify complete guidance %s bytes %d-%d: %w", chunk.Path, chunk.StartByte, chunk.EndByte, err)
			}
			text := strings.TrimSpace(result.Content)
			if text == "COVERED" {
				continue
			}
			if text == "" || isTruncatedFinishReason(result.FinishReason) {
				return "", fmt.Errorf("guidance verifier returned an incomplete ledger for %s bytes %d-%d", chunk.Path, chunk.StartByte, chunk.EndByte)
			}
			if provider.EstimateTokens(text, a.Model) > ledgerBudget {
				return "", fmt.Errorf("verified instruction ledger exceeds its %d-token continuation budget; no rules were silently dropped", ledgerBudget)
			}
			ledger = text
			changed = true
			break
		}
		if !changed {
			verified = true
			break
		}
	}
	if !verified {
		return "", fmt.Errorf("complete instruction ledger did not stabilize after %d full-source verification passes", guidanceVerificationPasses)
	}
	return rootprompt.CompiledGuidance(set.Digest(), set.TotalBytes(), set.Manifest(), ledger), nil
}

func (a *Agent) requestGuidanceCompilation(ctx context.Context, compilerProvider *provider.Provider, compilerModel string, maxOutput int, user string) (completionResult, error) {
	compiler := NewAgent(compilerProvider, compilerModel, a.logger)
	compiler.MaxRetries = a.MaxRetries
	compiler.MaxOutputTokens = maxOutput
	return compiler.completeStreamWithOptions(ctx, []provider.ChatMessage{
		provider.NewTextMessage("system", rootprompt.GuidanceCompilerSystem()),
		provider.NewTextMessage("user", user),
	}, nil, nil, nil, nil, nil, nil, nil, nil, completionRequestOptions{toolChoice: "none", maxOutputTokens: maxOutput})
}

func (g *projectGuidance) loadPaths(ctx context.Context, paths []string) (bool, error) {
	if g == nil || g.set == nil {
		return false, nil
	}
	return g.set.AddPaths(ctx, paths)
}

func (a *Agent) refreshProjectGuidance(ctx context.Context, systemPrompt string) (string, error) {
	if a.guidance == nil || a.guidance.set == nil {
		return "", nil
	}
	if systemPrompt == a.guidance.initialPrompt {
		systemPrompt = a.guidance.basePrompt
	}
	rendered, err := a.renderProjectGuidance(ctx, a.guidance.set, systemPrompt)
	if err != nil {
		return "", err
	}
	a.guidance.rendered = rendered
	a.guidance.digest = a.guidance.set.Digest()
	return rendered, nil
}

func guidanceCallPaths(calls []provider.ToolCall) ([]string, error) {
	var paths []string
	appendConcrete := func(tool, key, path string) error {
		if path == "" || strings.Contains(path, "://") {
			return nil
		}
		if strings.ContainsAny(path, "*?") {
			return &guidanceOperandError{message: fmt.Sprintf("invalid %s %s %q: wildcard syntax is unsupported in a literal path; pass a concrete path (glob_files uses pattern plus an optional concrete base_dir)", tool, key, path)}
		}
		if _, err := filepath.Abs(path); err != nil {
			return &guidanceOperandError{message: fmt.Sprintf("invalid %s %s %q: %v", tool, key, path, err)}
		}
		paths = append(paths, path)
		return nil
	}
	for _, call := range calls {
		if call.Function.Name == "apply_patch" && call.Type == "custom" {
			if patch, err := codexio.Parse(call.Function.Arguments); err == nil {
				for _, operation := range patch.Operations {
					if err := appendConcrete(call.Function.Name, "path", operation.Path); err != nil {
						return nil, err
					}
					if err := appendConcrete(call.Function.Name, "move_to", operation.MoveTo); err != nil {
						return nil, err
					}
				}
			}
			continue
		}
		var params map[string]any
		if json.Unmarshal([]byte(call.Function.Arguments), &params) != nil {
			continue
		}
		if call.Function.Name == "multi_tool_use.parallel" {
			if uses, ok := params["tool_uses"].([]any); ok {
				var children []provider.ToolCall
				for _, use := range uses {
					child, ok := use.(map[string]any)
					if !ok {
						continue
					}
					data, err := json.Marshal(child["parameters"])
					if err != nil {
						continue
					}
					name, _ := child["recipient_name"].(string)
					children = append(children, provider.ToolCall{Function: provider.FunctionCall{Name: strings.TrimPrefix(name, "functions."), Arguments: string(data)}})
				}
				childPaths, err := guidanceCallPaths(children)
				if err != nil {
					return nil, err
				}
				paths = append(paths, childPaths...)
			}
		}
		keys := []string{"path", "file_path", "source", "destination", "cwd", "workdir"}
		if call.Function.Name == "glob_files" {
			// The glob pattern is not a filesystem scope. Only its concrete
			// search anchor participates in guidance discovery.
			keys = []string{"base_dir"}
		}
		for _, key := range keys {
			if path, ok := params[key].(string); ok {
				if err := appendConcrete(call.Function.Name, key, path); err != nil {
					return nil, err
				}
			}
		}
		if call.Function.Name == "apply_patch" {
			input, _ := params["input"].(string)
			if patch, err := codexio.Parse(input); err == nil {
				for _, operation := range patch.Operations {
					if err := appendConcrete(call.Function.Name, "path", operation.Path); err != nil {
						return nil, err
					}
					if err := appendConcrete(call.Function.Name, "move_to", operation.MoveTo); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return paths, nil
}
