package session

import (
	"context"
	"errors"
	"fmt"
	"os"
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
// answer for one file each and release their server view afterwards.
type CodeIntelligence interface {
	EditorEnabled() bool
	ServesWarm(path string) bool
	ProbeDiagnostics(ctx context.Context, path string) ([]ttypes.LSPDiagnostic, bool, ttypes.LSPProbeView, error)
	ProbeDefinition(ctx context.Context, path string, line, col int) ([]ttypes.LSPLocation, ttypes.LSPProbeView, error)
	ProbeReferences(ctx context.Context, path string, line, col int) ([]ttypes.LSPLocation, ttypes.LSPProbeView, error)
	ProbeHover(ctx context.Context, path string, line, col int) (string, ttypes.LSPProbeView, error)
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
		if s.CodeIntelligenceEnabled() && s.controlToolSelected(live, "lsp") && live.Policy.IsAllowed(safety.OpRead) {
			definitions = appendUniqueDefinition(definitions, tools.CodeIntelligenceTool)
		}
		mu.Lock()
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
			if !s.controlToolSelected(live, "lsp") || !live.Policy.IsAllowed(safety.OpRead) {
				return tools.ToolResult{}, errors.New("the lsp tool is excluded by this role")
			}
			return s.executeLSPTool(ctx, live.WorkDir, params)
		}
		var edited []string
		if isEditTool(name) {
			edited = editedPaths(live.WorkDir, name, params)
		}
		result, err := previousFn(ctx, name, params)
		if err != nil || len(edited) == 0 || !s.CodeIntelligenceEnabled() {
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
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, path := range paths {
		if !service.ServesWarm(path) {
			continue
		}
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			diags, settled, view, err := service.ProbeDiagnostics(ctx, path)
			if err != nil || !settled {
				return
			}
			report := ""
			if errs := filterDiagnostics(diags, 1); len(errs) > 0 {
				report = formatFileDiagnostics(root, path, errs, view, true)
			}
			mu.Lock()
			out[path] = report
			mu.Unlock()
		}(path)
	}
	wg.Wait()
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
		var wg sync.WaitGroup
		for i, path := range resolved {
			wg.Add(1)
			go func(i int, path string) {
				defer wg.Done()
				diags, settled, view, err := service.ProbeDiagnostics(ctx, path)
				switch {
				case err != nil:
					reports[i] = fmt.Sprintf("%s: %v\n", displayLSPPath(root, path), err)
				default:
					report := formatFileDiagnostics(root, path, filterDiagnostics(diags, floor), view, false)
					if !settled {
						report += "  (analysis was still running when the request ended; results may be incomplete, call again to re-check)\n"
					}
					reports[i] = report
				}
			}(i, path)
		}
		wg.Wait()
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
	line, col, err := lspPosition(path, params)
	if err != nil {
		return tools.ToolResult{}, err
	}
	var locations []ttypes.LSPLocation
	var view ttypes.LSPProbeView
	switch operation {
	case "definition":
		locations, view, err = service.ProbeDefinition(ctx, path, line, col)
	case "references":
		locations, view, err = service.ProbeReferences(ctx, path, line, col)
	case "hover":
		text, view, err := service.ProbeHover(ctx, path, line, col)
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
	if err != nil {
		return tools.ToolResult{}, err
	}
	return tools.ToolResult{Content: formatLocations(root, operation, locations, view)}, nil
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
		if severity == 0 {
			severity = 1
		}
		if severity <= floor {
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

// lspPosition converts the 1-based line plus symbol or character column into
// the 0-based UTF-16 position the protocol uses.
func lspPosition(path string, params map[string]any) (int, int, error) {
	line := getIntParam(params, "line")
	if line < 1 {
		return 0, 0, errors.New("line (1-based) is required for this operation")
	}
	text, err := fileLine(path, line-1)
	if err != nil {
		return 0, 0, err
	}
	symbol := getStrParam(params, "symbol")
	column := getIntParam(params, "column")
	runes := []rune(text)
	var start int
	switch {
	case symbol != "":
		from := 0
		if column > 0 {
			from = min(column-1, len(runes))
		}
		offset := strings.Index(string(runes[from:]), symbol)
		if offset < 0 {
			return 0, 0, fmt.Errorf("symbol %q is not on line %d of %s: %q", symbol, line, filepath.Base(path), strings.TrimSpace(text))
		}
		start = from + utf8.RuneCountInString(string(runes[from:])[:offset])
	case column > 0:
		if column-1 > len(runes) {
			return 0, 0, fmt.Errorf("column %d is past the end of line %d (%d characters)", column, line, len(runes))
		}
		start = column - 1
	default:
		return 0, 0, errors.New("symbol (or column) is required for this operation")
	}
	return line - 1, len(utf16.Encode(runes[:start])), nil
}

func fileLine(path string, index int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if index < 0 || index >= len(lines) {
		return "", fmt.Errorf("%s has %d lines", filepath.Base(path), len(lines))
	}
	return strings.TrimRight(lines[index], "\r"), nil
}

// sourceLines caches file lines for one result so many locations in a file
// read it once.
type sourceLines map[string][]string

func (c sourceLines) line(path string, index int) (string, bool) {
	lines, ok := c[path]
	if !ok {
		data, err := os.ReadFile(path)
		if err == nil && utf8.Valid(data) {
			lines = strings.Split(string(data), "\n")
		}
		c[path] = lines
	}
	if index < 0 || index >= len(lines) {
		return "", false
	}
	return strings.TrimRight(lines[index], "\r"), true
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

func formatFileDiagnostics(root, path string, diags []ttypes.LSPDiagnostic, view ttypes.LSPProbeView, errorsOnly bool) string {
	var b strings.Builder
	b.WriteString(bufferNote(view))
	display := displayLSPPath(root, path)
	server := ""
	if view.Server != "" {
		server = " (" + view.Server + ")"
	}
	if len(diags) == 0 {
		fmt.Fprintf(&b, "%s: no problems%s\n", display, server)
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
	fmt.Fprintf(&b, "%s: %s%s\n", display, strings.Join(summary, ", "), server)
	lines := sourceLines{}
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
		fmt.Fprintf(&b, "  %d:%d %s%s%s\n", d.Line+1, lines.column(path, d.Line, d.Col), severity, message, origin)
		if quote := lines.quote(path, d.Line); quote != "" {
			fmt.Fprintf(&b, "      %s\n", quote)
		}
	}
	return b.String()
}

func formatLocations(root, operation string, locations []ttypes.LSPLocation, view ttypes.LSPProbeView) string {
	var b strings.Builder
	b.WriteString(bufferNote(view))
	if len(locations) == 0 {
		fmt.Fprintf(&b, "The language server found no %s at this position.", operation)
		return b.String()
	}
	noun := "definition"
	if operation == "references" {
		noun = "reference"
	}
	if len(locations) > 1 {
		noun += "s"
	}
	fmt.Fprintf(&b, "%d %s:\n", len(locations), noun)
	lines := sourceLines{}
	for i, loc := range locations {
		if i == maxLSPLocations {
			fmt.Fprintf(&b, "… %d more not shown\n", len(locations)-i)
			break
		}
		path := loc.Path
		fmt.Fprintf(&b, "%s:%d:%d", displayLSPPath(root, path), loc.Line+1, lines.column(path, loc.Line, loc.Col))
		if quote := lines.quote(path, loc.Line); quote != "" {
			fmt.Fprintf(&b, "  %s", quote)
		}
		b.WriteByte('\n')
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
