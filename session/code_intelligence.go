package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"crude/agent"
	"crude/codexio"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/tools"
	ttypes "crude/types"
)

// CodeIntelligence is the workspace language service a session borrows. Probes
// retain their borrowed source views until the complete request has finished.
type CodeIntelligence interface {
	EditorEnabled() bool
	ServesWarm(path string) bool
	ProbeDiagnosticsBatch(ctx context.Context, paths []string) []ttypes.LSPDiagnosticsResult
	ProbeNavigation(ctx context.Context, path, kind string, position ttypes.LSPSourcePosition, includeDeclaration bool) (ttypes.LSPNavigation, ttypes.LSPProbeView, error)
	ProbeHover(ctx context.Context, path string, position ttypes.LSPSourcePosition) (string, ttypes.LSPProbeView, error)
	ProbeSymbols(ctx context.Context, path string) ([]ttypes.LSPSymbol, ttypes.LSPProbeView, error)
}

const (
	// codeIntelligenceWait bounds one lsp call. A cold server indexing a large
	// project can take this long; a slower answer is reported as unsettled.
	codeIntelligenceWait = 60 * time.Second
	// postEditDiagnosticsWait bounds the automatic check after an edit. It runs
	// only against an already warm server, so it is a latency ceiling, not a
	// typical cost.
	postEditDiagnosticsWait = 3 * time.Second
	maxLSPPaths             = 10
	maxLSPLocations         = 50
	maxLSPDiagnostics       = 50
	maxLSPSymbols           = 200
	maxHoverBytes           = 4096
)

func (s *Session) SetCodeIntelligence(service CodeIntelligence) {
	s.mu.Lock()
	s.codeIntel = service
	s.mu.Unlock()
}

// CodeIntelligenceEnabled is the session override when set, else the
// workspace's LSP switch.
func (s *Session) CodeIntelligenceEnabled() bool {
	s.mu.Lock()
	service, override := s.codeIntel, s.codeIntelOverride
	s.mu.Unlock()
	if service == nil {
		return false
	}
	if override != nil {
		return *override
	}
	return service.EditorEnabled()
}

func (s *Session) codeIntelligence() CodeIntelligence {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.codeIntel
}

func (s *Session) codeIntelligenceConsumerAllowed(live *agent.Agent) bool {
	return s.controlToolSelected(live, "lsp") && live.Policy.IsAllowed(safety.OpRead)
}

// bindCodeIntelligence offers the lsp tool while code intelligence is on and
// queues post-edit diagnostics for the next request. The tool set changes only
// when the setting changes, so prompt caching is unaffected otherwise.
func (s *Session) bindCodeIntelligence(live *agent.Agent) {
	previousPrepare, previousFn := live.PrepareStep, live.ToolFn
	var mu sync.Mutex
	pending := map[string]string{}
	live.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		var definitions []tools.ToolDef
		var notices []provider.ChatMessage
		if previousPrepare != nil {
			definitions, notices = previousPrepare()
		} else {
			definitions = append(definitions, live.FilteredTools...)
		}
		allowed := s.CodeIntelligenceEnabled() && s.codeIntelligenceConsumerAllowed(live)
		if allowed {
			definitions = appendUniqueDefinition(definitions, tools.CodeIntelligenceTool)
		}
		mu.Lock()
		if !allowed {
			clear(pending)
		}
		if len(pending) > 0 {
			paths := make([]string, 0, len(pending))
			for path := range pending {
				paths = append(paths, path)
			}
			sort.Strings(paths)
			var body strings.Builder
			for _, path := range paths {
				body.WriteString(pending[path])
			}
			clear(pending)
			notices = append(notices, runtimeMessage(rootprompt.RuntimeDiagnostics(strings.TrimRight(body.String(), "\n"))))
		}
		mu.Unlock()
		return definitions, notices
	}
	live.ToolFn = func(ctx context.Context, name string, params map[string]any) (tools.ToolResult, error) {
		if name == "lsp" {
			if !s.CodeIntelligenceEnabled() {
				return tools.ToolResult{}, errors.New("code intelligence is off for this session; it can be turned on in session settings (/settings) or with settings.set scope=session path=code_intelligence")
			}
			if !s.codeIntelligenceConsumerAllowed(live) {
				return tools.ToolResult{}, errors.New("the lsp tool is excluded by this role or read policy")
			}
			return s.executeLSPTool(ctx, live.WorkDir, params)
		}
		var edited []string
		if isEditTool(name) {
			edited = editedPaths(live.WorkDir, name, params)
		}
		result, err := previousFn(ctx, name, params)
		if err != nil || len(edited) == 0 || !s.CodeIntelligenceEnabled() || !s.codeIntelligenceConsumerAllowed(live) {
			return result, err
		}
		for path, report := range s.postEditDiagnostics(ctx, live.WorkDir, edited) {
			mu.Lock()
			if report == "" {
				delete(pending, path)
			} else {
				pending[path] = report
			}
			mu.Unlock()
		}
		return result, err
	}
}

func isEditTool(name string) bool {
	switch name {
	case "apply_patch", "write_file", "edit_file", "move_file", "copy_file":
		return true
	}
	return false
}

func editedPaths(root, name string, params map[string]any) []string {
	var paths []string
	switch name {
	case "apply_patch":
		input := getStrParam(params, "input")
		if patch, err := codexio.Parse(input); err == nil {
			paths = patch.Paths(root)
		}
	case "move_file", "copy_file":
		paths = []string{getStrParam(params, "destination")}
	default:
		paths = []string{getStrParam(params, "path")}
	}
	out := paths[:0]
	for _, path := range paths {
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		out = append(out, filepath.Clean(path))
	}
	return out
}

// postEditDiagnostics reports errors only, only from servers that are already
// running for the file, and only once analysis settles. An unsettled or clean
// result yields "" so an earlier report for that file is withdrawn.
func (s *Session) postEditDiagnostics(ctx context.Context, root string, paths []string) map[string]string {
	service := s.codeIntelligence()
	out := map[string]string{}
	if service == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, postEditDiagnosticsWait)
	defer cancel()
	var warm []string
	for _, path := range paths {
		if service.ServesWarm(path) {
			warm = append(warm, path)
		}
	}
	for i, result := range service.ProbeDiagnosticsBatch(ctx, warm) {
		batch, view := result.Diagnostics, result.View
		if result.Error != "" || !result.Settled || !batch.HasExactSource() || batch.Boundary.Source != view.Source {
			continue
		}
		report := ""
		if errs := filterDiagnostics(batch.Diagnostics, 1); len(errs) > 0 {
			report = formatFileDiagnostics(root, warm[i], batch, 1, view, true)
		}
		out[warm[i]] = report
	}
	return out
}

func (s *Session) executeLSPTool(ctx context.Context, root string, params map[string]any) (tools.ToolResult, error) {
	service := s.codeIntelligence()
	if service == nil {
		return tools.ToolResult{}, errors.New("this workspace has no language service")
	}
	ctx, cancel := context.WithTimeout(ctx, codeIntelligenceWait)
	defer cancel()
	operation := getStrParam(params, "operation")
	if operation == "diagnostics" {
		paths := stringListParam(params, "paths")
		if path := getStrParam(params, "path"); path != "" {
			paths = append([]string{path}, paths...)
		}
		if len(paths) == 0 {
			return tools.ToolResult{}, errors.New("diagnostics needs path or paths")
		}
		if len(paths) > maxLSPPaths {
			return tools.ToolResult{}, fmt.Errorf("diagnostics checks at most %d files per call; split the request", maxLSPPaths)
		}
		floor, err := severityFloor(getStrParam(params, "severity"))
		if err != nil {
			return tools.ToolResult{}, err
		}
		resolved := make([]string, len(paths))
		for i, path := range paths {
			if resolved[i], err = resolveLSPPath(root, path); err != nil {
				return tools.ToolResult{}, err
			}
		}
		reports := make([]string, len(resolved))
		for i, result := range service.ProbeDiagnosticsBatch(ctx, resolved) {
			switch {
			case result.Error != "":
				reports[i] = fmt.Sprintf("%s: %s\n", displayLSPPath(root, resolved[i]), result.Error)
			default:
				report := formatFileDiagnostics(root, resolved[i], result.Diagnostics, floor, result.View, false)
				if !result.Settled {
					report += "  The check did not finish. Run diagnostics again for this file.\n"
				}
				reports[i] = report
			}
		}
		return tools.ToolResult{Content: strings.TrimRight(strings.Join(reports, ""), "\n")}, nil
	}

	path, err := resolveLSPPath(root, getStrParam(params, "path"))
	if err != nil {
		return tools.ToolResult{}, err
	}
	if operation == "symbols" {
		symbols, view, err := service.ProbeSymbols(ctx, path)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return tools.ToolResult{Content: formatSymbols(root, path, symbols, view)}, nil
	}
	position := ttypes.LSPSourcePosition{Line: getIntParam(params, "line"), Column: getIntParam(params, "column"), Symbol: getStrParam(params, "symbol")}
	switch operation {
	case ttypes.LSPNavigationDefinition, ttypes.LSPNavigationTypeDefinition, ttypes.LSPNavigationImplementation, ttypes.LSPNavigationReferences:
		result, view, err := service.ProbeNavigation(ctx, path, operation, position, operation == ttypes.LSPNavigationReferences)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return tools.ToolResult{Content: formatNavigation(root, result, view)}, nil
	case "hover":
		text, view, err := service.ProbeHover(ctx, path, position)
		if err != nil {
			return tools.ToolResult{}, err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			text = "The language server has no hover information at this position."
		}
		if len(text) > maxHoverBytes {
			text = truncateUTF8(text, maxHoverBytes) + "\n… (truncated)"
		}
		return tools.ToolResult{Content: bufferNote(view) + text}, nil
	default:
		return tools.ToolResult{}, fmt.Errorf("unknown lsp operation %q", operation)
	}
}

func severityFloor(value string) (int, error) {
	switch value {
	case "error":
		return 1, nil
	case "", "warning":
		return 2, nil
	case "all":
		return 4, nil
	}
	return 0, fmt.Errorf("severity must be error, warning or all")
}

// filterDiagnostics keeps severities up to floor (1 error … 4 hint). A server
// that omits severity is treated as reporting an error, per the protocol.
func filterDiagnostics(diags []ttypes.LSPDiagnostic, floor int) []ttypes.LSPDiagnostic {
	out := make([]ttypes.LSPDiagnostic, 0, len(diags))
	for _, d := range diags {
		severity := d.Severity
		if severity >= 1 && severity <= floor {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
	return out
}

// resolveLSPPath keeps agent probes inside the workspace, where the language
// servers' roots live.
func resolveLSPPath(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	if root != "" {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%s is outside the workspace", path)
		}
	}
	return path, nil
}

func displayLSPPath(root, path string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

// sourceLines quotes only immutable source actually leased by the answer owner.
// Destination files without their own qualified source remain raw UTF-16 data.
type sourceLines map[string][]string

func probeSourceLines(view ttypes.LSPProbeView) sourceLines {
	lines := sourceLines{}
	if view.Source.DocumentID != "" && view.Source.Path != "" {
		lines[view.Source.Path] = strings.Split(view.SourceText, "\n")
	}
	return lines
}
func (c sourceLines) line(path string, index int) (string, bool) {
	lines, ok := c[path]
	if !ok || index < 0 || index >= len(lines) {
		return "", false
	}
	return strings.TrimSuffix(lines[index], "\r"), true
}

// column converts a UTF-16 offset on a source line to a 1-based character
// column.
func (c sourceLines) column(path string, index, utf16Col int) int {
	text, ok := c.line(path, index)
	if !ok {
		return utf16Col + 1
	}
	units := 0
	for i, r := range []rune(text) {
		if units >= utf16Col {
			return i + 1
		}
		units += utf16.RuneLen(r)
	}
	return utf8.RuneCountInString(text) + 1
}

func (c sourceLines) quote(path string, index int) string {
	text, ok := c.line(path, index)
	if !ok {
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) > 160 {
		text = truncateUTF8(text, 160) + "…"
	}
	return text
}

func truncateUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func bufferNote(view ttypes.LSPProbeView) string {
	if view.BufferDiffers {
		return "Note: this file has unsaved changes in the editor; results describe the editor buffer, not the file on disk.\n"
	}
	return ""
}

var severityNames = map[int]string{1: "error", 2: "warning", 3: "info", 4: "hint"}

func formatFileDiagnostics(root, path string, batch ttypes.InsightDiagnostics, floor int, view ttypes.LSPProbeView, errorsOnly bool) string {
	diags := filterDiagnostics(batch.Diagnostics, floor)
	var b strings.Builder
	display := displayLSPPath(root, path)
	server := ""
	if view.Server != "" {
		server = " (" + view.Server + ")"
	}
	if batch.ID == "" {
		fmt.Fprintf(&b, "%s: the language server%s has not reported this file yet; call again to re-check.\n", display, server)
		return b.String()
	}
	// One line per file. Fact identities, context hashes and the fixed list
	// of unknown inputs stay in the structured facts: they cost every
	// agent tokens and change nothing it does. Only qualifications that change
	// how to read the result are written.
	var notes []string
	if batch.State == ttypes.InsightStale {
		notes = append(notes, "out of date")
	}
	if batch.Binding != "exact-version" {
		notes = append(notes, "the server did not confirm which file version it checked")
	}
	if batch.Detail != "" {
		notes = append(notes, strings.TrimSuffix(strings.TrimSpace(batch.Detail), "."))
	}
	if view.BufferDiffers {
		notes = append(notes, "checked the unsaved editor text, not the file on disk")
	}
	note := ""
	if len(notes) > 0 {
		note = " — " + strings.Join(notes, "; ")
	}
	if len(diags) == 0 {
		fmt.Fprintf(&b, "%s: no problems at the requested severity%s%s\n", display, server, note)
		return b.String()
	}
	counts := map[string]int{}
	for _, d := range diags {
		counts[severityNames[max(1, d.Severity)]]++
	}
	var summary []string
	for _, name := range []string{"error", "warning", "info", "hint"} {
		if n := counts[name]; n > 0 {
			label := name
			if n > 1 {
				label += "s"
			}
			summary = append(summary, fmt.Sprintf("%d %s", n, label))
		}
	}
	fmt.Fprintf(&b, "%s: %s%s%s\n", display, strings.Join(summary, ", "), server, note)
	lines := sourceLines{}
	if batch.HasExactSource() && batch.Boundary.Source == view.Source {
		lines = probeSourceLines(view)
	}
	for i, d := range diags {
		if i == maxLSPDiagnostics {
			fmt.Fprintf(&b, "  … %d more not shown\n", len(diags)-i)
			break
		}
		origin := strings.TrimSpace(strings.Join([]string{d.Source, d.Code}, " "))
		if origin != "" {
			origin = " [" + origin + "]"
		}
		severity := ""
		if !errorsOnly {
			severity = severityNames[max(1, d.Severity)] + " "
		}
		message := strings.ReplaceAll(strings.TrimSpace(d.Message), "\n", " ")
		coordinate := fmt.Sprintf("%d:%d", d.Line+1, lines.column(path, d.Line, d.Col))
		if _, ok := lines[path]; !ok {
			// Without the checked source the column is the server's UTF-16
			// offset, which differs from characters after non-ASCII text.
			coordinate = fmt.Sprintf("%d:%d (UTF-16 column)", d.Line+1, d.Col+1)
		}
		fmt.Fprintf(&b, "  %s %s%s%s\n", coordinate, severity, message, origin)
		if quote := lines.quote(path, d.Line); quote != "" {
			fmt.Fprintf(&b, "      %s\n", quote)
		}
	}
	return b.String()
}

// formatNavigation lists the answer the way an agent uses it: one-based
// path:line:column per location, with the source line when the queried source
// contains it. Destination files are never read; a location in another file
// keeps the server's UTF-16 column. Provenance identities stay in the
// structured facts.
func formatNavigation(root string, result ttypes.LSPNavigation, view ttypes.LSPProbeView) string {
	var b strings.Builder
	b.WriteString(bufferNote(view))
	source := result.Boundary.Source
	lines := sourceLines{}
	if source.DocumentID != "" && source == view.Source {
		lines = probeSourceLines(view)
	}
	query := fmt.Sprintf("%s %s:%d:%d", strings.ReplaceAll(result.Kind, "-", " "), displayLSPPath(root, source.Path), result.Line+1, lines.column(source.Path, result.Line, result.Col))
	switch result.Answer {
	case ttypes.LSPNavigationNull:
		fmt.Fprintf(&b, "%s: the language server returned no result.", query)
		return b.String()
	case ttypes.LSPNavigationEmpty:
		fmt.Fprintf(&b, "%s: no locations.", query)
		return b.String()
	case ttypes.LSPNavigationUnsupported:
		fmt.Fprintf(&b, "%s: the language server does not support %s.", query, strings.ReplaceAll(result.Kind, "-", " "))
		return b.String()
	}
	fmt.Fprintf(&b, "%s: %d %s\n", query, len(result.Targets), map[bool]string{true: "location", false: "locations"}[len(result.Targets) == 1])
	utf16 := false
	for i, target := range result.Targets {
		if i == maxLSPLocations {
			fmt.Fprintf(&b, "  … %d more not shown\n", len(result.Targets)-i)
			break
		}
		start := target.Selection.Start
		if target.Path == "" {
			fmt.Fprintf(&b, "  %s", target.URI)
		} else {
			_, known := lines[target.Path]
			column := start.Character + 1
			if known {
				column = lines.column(target.Path, start.Line, start.Character)
			} else {
				utf16 = true
			}
			fmt.Fprintf(&b, "  %s:%d:%d", displayLSPPath(root, target.Path), start.Line+1, column)
		}
		switch {
		case target.UnavailableReason != "":
			fmt.Fprintf(&b, " (cannot open: %s)", target.UnavailableReason)
		case target.External:
			b.WriteString(" (outside the workspace)")
		default:
			if quote := lines.quote(target.Path, start.Line); quote != "" {
				fmt.Fprintf(&b, "  %s", quote)
			}
		}
		b.WriteByte('\n')
	}
	if utf16 {
		b.WriteString("Columns in other files count UTF-16 units.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatSymbols(root, path string, symbols []ttypes.LSPSymbol, view ttypes.LSPProbeView) string {
	var b strings.Builder
	b.WriteString(bufferNote(view))
	if len(symbols) == 0 {
		fmt.Fprintf(&b, "%s: the language server reported no symbols.", displayLSPPath(root, path))
		return b.String()
	}
	fmt.Fprintf(&b, "%s:\n", displayLSPPath(root, path))
	written := 0
	var walk func([]ttypes.LSPSymbol, int)
	walk = func(items []ttypes.LSPSymbol, depth int) {
		for _, symbol := range items {
			if written == maxLSPSymbols {
				return
			}
			written++
			kind := strings.ToLower(symbol.KindName)
			detail := ""
			if symbol.Detail != "" {
				detail = " " + strings.ReplaceAll(symbol.Detail, "\n", " ")
			}
			fmt.Fprintf(&b, "%s%s %s%s  %d\n", strings.Repeat("  ", depth+1), kind, symbol.Name, detail, symbol.Line+1)
			walk(symbol.Children, depth+1)
		}
	}
	walk(symbols, 0)
	if written == maxLSPSymbols {
		fmt.Fprintf(&b, "  … outline truncated at %d symbols\n", maxLSPSymbols)
	}
	return strings.TrimRight(b.String(), "\n")
}
