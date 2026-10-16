package safety

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"crude/logger"

	"github.com/mattn/go-shellwords"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type CommandRisk int

const (
	RiskUnknown      CommandRisk = iota
	RiskSafe                     // auto-allow: ls, cat, grep, pwd, echo, etc.
	RiskProjectWrite             // confirm if outside project: rm file, mv, cp, mkdir
	RiskNetwork                  // confirm: curl, wget, ssh, netcat, git operations
	RiskDestructive              // always confirm: rm -rf, dd, mkfs, sudo
	RiskBlocked                  // never allow: rm -rf /, dd to block dev, fork bomb
)

func (r CommandRisk) String() string {
	switch r {
	case RiskSafe:
		return "safe"
	case RiskProjectWrite:
		return "project-write"
	case RiskNetwork:
		return "network"
	case RiskDestructive:
		return "destructive"
	case RiskBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

func (r CommandRisk) NeedsConfirmation() bool {
	return r >= RiskProjectWrite
}

func (r CommandRisk) IsAlwaysBlocked() bool {
	return r == RiskBlocked
}

type CommandSafetyResult struct {
	Risk                 CommandRisk
	BaseCommand          string
	FullCommand          string
	Reason               string
	Suggestion           string
	TargetsSystem        bool
	UsesSudo             bool
	PipedToShell         bool
	ModifiesConfig       bool
	ReadsCredentials     bool
	Obfuscated           bool
	AffectedPaths        []string
	RedirectTargets      []string
	Calls                []string
	MatchedRule          string
	ConfirmationRequired bool
}

func (r *CommandSafetyResult) NeedsConfirmation() bool {
	return r != nil && r.ConfirmationRequired && !r.Risk.IsAlwaysBlocked()
}

var systemPrefixes []string

func init() {
	systemPrefixes = buildSystemPrefixes()
}

func buildSystemPrefixes() []string {
	posixPrefixes := []string{
		"/", "/etc", "/dev", "/proc", "/sys", "/boot", "/usr", "/bin",
		"/sbin", "/lib", "/lib64", "/opt", "/root", "/var/log",
		"/var/lib", "/var/spool",
	}

	switch runtime.GOOS {
	case "windows":
		sysDrive := os.Getenv("SystemDrive")
		if sysDrive == "" {
			sysDrive = "C:"
		}
		return append(posixPrefixes,
			filepath.Clean(sysDrive+`\Windows`),
			filepath.Clean(sysDrive+`\Program Files`),
			filepath.Clean(sysDrive+`\Program Files (x86)`),
			filepath.Clean(sysDrive+`\ProgramData`),
			filepath.Clean(sysDrive+`\Users\Default`),
			filepath.Clean(sysDrive+`\Recovery`),
		)
	default:
		return posixPrefixes
	}
}

func isPathUnderPrefix(path string, prefix string) bool {
	cleaned := filepath.Clean(path)
	// On Windows, filepath.IsAbs returns false for POSIX-style root-relative
	// paths like \etc\config.conf (no drive letter). These are still "absolute"
	// in the sense that they refer to fixed system locations from the current
	// drive's root, and the Windows-specific block below handles them. Skip
	// the IsAbs guard for paths anchored at a path separator so that block
	// is reachable.
	rootAnchored := strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "\\")
	if !filepath.IsAbs(cleaned) && !rootAnchored {
		return false
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		abs = cleaned
	}
	cleanPrefix := filepath.Clean(prefix)
	absPrefix, err := filepath.Abs(cleanPrefix)
	if err != nil {
		absPrefix = cleanPrefix
	}

	if runtime.GOOS == "windows" {
		if rootAnchored {
			// POSIX-style path on Windows (\etc\config.conf, \, etc.).
			// Match against POSIX-style prefixes only; Windows drive
			// prefixes (C:\Windows) don't apply to root-relative paths.
			if cleanPrefix == "/" || cleanPrefix == "\\" {
				return cleaned == cleanPrefix
			}
			if !strings.HasPrefix(cleaned, cleanPrefix) {
				return false
			}
			return len(cleaned) == len(cleanPrefix) || cleaned[len(cleanPrefix)] == filepath.Separator
		}
		if len(abs) < len(absPrefix) {
			return false
		}
		if !strings.EqualFold(abs[:len(absPrefix)], absPrefix) {
			return false
		}
		return len(abs) == len(absPrefix) || abs[len(absPrefix)] == filepath.Separator
	}

	if absPrefix == string(filepath.Separator) {
		// Protect the root itself. Its descendants are classified by the
		// specific system directories, not by being absolute paths.
		return abs == absPrefix
	}
	if !strings.HasPrefix(abs, absPrefix) {
		return false
	}
	return len(abs) == len(absPrefix) || abs[len(absPrefix)] == filepath.Separator
}

// isStreamDevicePath reports whether a path names a stream pseudo-device that
// cannot damage the system when read or written (`2>/dev/null`, `<(…)` paths).
func isStreamDevicePath(path string) bool {
	if strings.EqualFold(path, "NUL") {
		return true
	}
	switch filepath.ToSlash(filepath.Clean(path)) {
	case "/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom",
		"/dev/stdin", "/dev/stdout", "/dev/stderr", "/dev/tty":
		return true
	}
	return strings.HasPrefix(filepath.ToSlash(path), "/dev/fd/")
}

func isBlockDevicePath(path string) bool {
	cleaned := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		lower := strings.ToLower(cleaned)
		if strings.HasPrefix(lower, `\\.\physicaldrive`) ||
			strings.HasPrefix(lower, `\\.\cdrom`) {
			return true
		}
		if strings.HasPrefix(cleaned, "\\dev\\") {
			return true
		}
	}
	return strings.HasPrefix(cleaned, "/dev/sd") ||
		strings.HasPrefix(cleaned, "/dev/nvme") ||
		strings.HasPrefix(cleaned, "/dev/mmcblk") ||
		strings.HasPrefix(cleaned, "/dev/loop")
}

var configFilePatterns = []string{
	".bashrc", ".zshrc", ".profile", ".bash_profile",
	".bash_login", ".zprofile", ".config/fish",
	".gitconfig", ".git-credentials", ".ssh/",
	"cron", "crontab", "systemd", ".service",
	".vimrc", ".tmux.conf", ".inputrc",
	".bash_logout", ".zshenv", ".zlogin",
}

var credPattern = regexp.MustCompile(`(?i)(~\/\.ssh|~\/\.aws|~\/\.gcp|~\/\.azure|~\/\.config/gcloud|~\/\.docker|/etc/shadow|/etc/passwd|\$[A-Z_]+(KEY|SECRET|TOKEN|PASS|CREDENTIAL|API))`)

var (
	cdPrefixRe  = regexp.MustCompile(`(?i)^cd\s+(?:/d\s+)?(?:\S+|"[^"]*"|'[^']*')\s*&&\s*`)
	cmdPrefixRe = regexp.MustCompile(`(?i)^cmd\s+/c\s+`)
)

// shellInterpreters can execute piped or scripted input as code.
var shellInterpreters = []string{"sh", "bash", "zsh", "dash", "fish", "ksh", "ash"}

// shellNames additionally lists `source`, which executes code but cannot
// consume a pipeline.
var shellNames = append(append([]string{}, shellInterpreters...), "source")

// remoteSourceCommands can move code in from a network peer. Piped into a
// shell they are a supply-chain risk; anywhere else they are network access.
var remoteSourceCommands = map[string]bool{
	"curl": true, "wget": true, "fetch": true, "xh": true, "httpie": true,
	"ssh": true, "scp": true, "sftp": true,
	"nc": true, "ncat": true, "netcat": true, "socat": true,
}

// remotePipeToShellRe is the text fallback for commands the parser rejects;
// everything that parses is classified from the syntax tree, so command text
// that is only data (heredoc bodies, quoted arguments) cannot trigger it.
var remotePipeToShellRe = regexp.MustCompile(`(?i)\b(curl|wget|ssh|scp|sftp|nc|ncat|netcat|socat|fetch|xh)\b[^|]*\|\s*(bash|sh|zsh|dash|fish|ksh|ash)\b`)

// symbolicGetenv resolves variables this process can see. Variables set only
// for the command (CRUDE_TEMP_DIR, a preceding assignment) stay as `$NAME`, so
// "$CRUDE_TEMP_DIR/out" is never read as the root-level path "/out".
func symbolicGetenv(name string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return "$" + name
}

var errProcSubstUnresolved = errors.New("process substitution is not resolved")

// expandCfg resolves words without running anything. The expander calls
// ProcSubst unconditionally, so it must be set: `<(cmd)` stays unresolved and
// the word falls back to its printed form, while the walker still classifies
// the commands inside it.
var expandCfg = &expand.Config{
	Env: expand.FuncEnviron(symbolicGetenv),
	ProcSubst: func(ps *syntax.ProcSubst) (string, error) {
		return "", errProcSubstUnresolved
	},
}

// preprocessCommand prepares a command for the bash parser. On POSIX a
// backslash is shell syntax (escapes, `\`+newline continuations), so it is
// kept. Windows commands use it as the path separator; it becomes `/` there,
// except before a line break, where it still continues the line.
func preprocessCommand(cmd string) string {
	normalized := cmd
	if runtime.GOOS == "windows" {
		normalized = windowsSeparatorsToSlash(normalized)
	}
	for cdPrefixRe.MatchString(normalized) {
		normalized = cdPrefixRe.ReplaceAllString(normalized, "")
	}
	normalized = cmdPrefixRe.ReplaceAllString(normalized, "")
	return normalized
}

func windowsSeparatorsToSlash(cmd string) string {
	var b strings.Builder
	b.Grow(len(cmd))
	for i := 0; i < len(cmd); i++ {
		if cmd[i] == '\\' && (i+1 == len(cmd) || (cmd[i+1] != '\n' && cmd[i+1] != '\r')) {
			b.WriteByte('/')
			continue
		}
		b.WriteByte(cmd[i])
	}
	return b.String()
}

func extractCmdsSimple(cmd string) (calls []string, pipedToShell bool, redirectTargets []string, allArgs []string) {
	redirectTargets = extractRedirectsSimple(cmd)

	pipeSegs := splitOnPipes(cmd)

	for i, seg := range pipeSegs {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		tokens, err := shellwords.Parse(seg)
		if err != nil || len(tokens) == 0 {
			continue
		}
		calls = append(calls, tokens[0])
		allArgs = append(allArgs, tokens...)
		if i > 0 {
			lowBase := strings.ToLower(tokens[0])
			for _, s := range shellNames {
				if lowBase == s {
					pipedToShell = true
					break
				}
			}
		}
	}

	return
}

var redirectRe = regexp.MustCompile(`(?:^|\s)(>>?)\s+(\S+)`)

func extractRedirectsSimple(cmd string) []string {
	var targets []string
	matches := redirectRe.FindAllStringSubmatch(cmd, -1)
	for _, m := range matches {
		if len(m) >= 3 {
			targets = append(targets, m[2])
		}
	}
	return targets
}

func splitOnPipes(cmd string) []string {
	const orPlaceholder = "\x00OR\x00"
	normalized := strings.ReplaceAll(cmd, "||", orPlaceholder)
	parts := strings.Split(normalized, "|")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(p, orPlaceholder, "||")
	}
	return parts
}

func wordString(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	if lit := w.Lit(); lit != "" {
		return lit
	}
	var buf bytes.Buffer
	_ = syntax.NewPrinter().Print(&buf, w)
	return strings.TrimSpace(buf.String())
}

func resolveWord(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	if lit := w.Lit(); lit != "" {
		return lit
	}
	expanded, err := expand.Literal(expandCfg, w)
	if err != nil {
		return wordString(w)
	}
	return expanded
}

func isObfuscatedWord(w *syntax.Word) bool {
	if w == nil || len(w.Parts) == 0 {
		return false
	}
	if w.Lit() != "" {
		return false
	}
	for _, part := range w.Parts {
		switch part.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp:
			return true
		}
	}
	return false
}

// extractCmds walks a parsed command and reports the commands it would run.
//
// pipedToShell is true only when an interpreter consumes a pipe (`fetch | sh`).
// A shell that merely appears inside an && list, or whose output is piped
// somewhere else, is not a sink, so ordinary tooling wrappers such as
// `cd x && bash -c '...' | tail` are never mistaken for remote code execution.
func extractCmds(prog *syntax.File) (calls []string, pipedToShell bool, redirectTargets []string, stmts []*syntax.CallExpr, obfuscated bool) {
	syntax.Walk(prog, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				if isShellSink(callExpr(n.Y)) {
					pipedToShell = true
				}
			}
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				return true
			}
			calls = append(calls, resolveWord(n.Args[0]))
			stmts = append(stmts, n)
			if isObfuscatedWord(n.Args[0]) {
				obfuscated = true
			}
		case *syntax.Redirect:
			if !writesFile(n.Op) {
				return true
			}
			target := resolveWord(n.Word)
			if target != "" {
				redirectTargets = append(redirectTargets, target)
			}
		}
		return true
	})
	return
}

// writesFile reports whether a redirect opens its target for writing. Input
// redirects and descriptor duplication (`>&2`) never modify a path.
func writesFile(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.AppClob, syntax.RdrAll, syntax.AppAll,
		syntax.RdrAllClob, syntax.AppAllClob, syntax.RdrInOut:
		return true
	}
	return false
}

func callExpr(stmt *syntax.Stmt) *syntax.CallExpr {
	if stmt == nil {
		return nil
	}
	call, _ := stmt.Cmd.(*syntax.CallExpr)
	return call
}

// wrapperCommands launch another command and can therefore sit between a pipe
// and an interpreter (`curl … | sudo bash`, `curl … | env bash`).
var wrapperCommands = map[string]bool{
	"sudo": true, "doas": true, "env": true, "nohup": true, "nice": true,
	"ionice": true, "setsid": true, "stdbuf": true, "time": true,
	"exec": true, "command": true, "xargs": true,
}

// isShellSink reports whether the command on the receiving end of a pipe
// interprets that input as code, including through exec wrappers.
func isShellSink(call *syntax.CallExpr) bool {
	if call == nil || len(call.Args) == 0 {
		return false
	}
	first := strings.ToLower(filepath.Base(resolveWord(call.Args[0])))
	if isShellInterpreter(first) {
		return true
	}
	if !wrapperCommands[first] {
		return false
	}
	for _, arg := range call.Args[1:] {
		if isShellInterpreter(strings.ToLower(filepath.Base(resolveWord(arg)))) {
			return true
		}
	}
	return false
}

// isShellInterpreter reports whether a command name can execute piped input.
func isShellInterpreter(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	for _, shell := range shellInterpreters {
		if base == shell {
			return true
		}
	}
	return false
}

// pipelineFetchesRemote reports whether any command in a pipeline can move
// code in from a network peer.
func pipelineFetchesRemote(calls []string) bool {
	for _, call := range calls {
		base := strings.ToLower(filepath.Base(strings.TrimSpace(call)))
		if remoteSourceCommands[base] {
			return true
		}
	}
	return false
}

func checkCommandSafetyBase(cmd string, lg *logger.Logger) *CommandSafetyResult {
	res := &CommandSafetyResult{
		FullCommand: cmd,
	}

	if strings.TrimSpace(cmd) == "" {
		res.Risk = RiskBlocked
		res.Reason = "Empty command"
		return res
	}

	preprocessed := preprocessCommand(cmd)

	var calls []string
	var pipedToShell bool
	var redirectTargets []string
	var allArgs []string
	var shellCalls []shellCall
	var prog *syntax.File

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	parsedProg, parseErr := parser.Parse(strings.NewReader(preprocessed), "")
	if parseErr == nil {
		var stmts []*syntax.CallExpr
		var obfuscated bool
		calls, pipedToShell, redirectTargets, stmts, obfuscated = extractCmds(parsedProg)
		res.Obfuscated = obfuscated
		allArgs = collectAllArgs(stmts)
		for _, stmt := range stmts {
			call := shellCall{args: make([]string, len(stmt.Args))}
			for i, w := range stmt.Args {
				call.args[i] = resolveWord(w)
			}
			shellCalls = append(shellCalls, call)
		}
		prog = parsedProg
	} else {
		calls, pipedToShell, redirectTargets, allArgs = extractCmdsSimple(preprocessed)
		// Unparsed text cannot be split into argument lists reliably, so each
		// command is judged against every argument: never less strict.
		for _, name := range calls {
			shellCalls = append(shellCalls, shellCall{args: append([]string{name}, allArgs...)})
		}
		if len(calls) == 0 {
			res.Risk = RiskUnknown
			res.Reason = fmt.Sprintf("Could not parse or extract command: %v", parseErr)
			lg.Debug("COMMAND", "parse", "error", parseErr.Error(), "risk", res.Risk.String())
			return res
		}
		lg.Debug("COMMAND", "fallback_parse", "original_error", parseErr.Error(), "extracted_calls", fmt.Sprintf("%v", calls))
	}

	res.Calls = calls
	res.PipedToShell = pipedToShell
	res.RedirectTargets = redirectTargets

	for _, rt := range redirectTargets {
		if isStreamDevicePath(rt) {
			continue
		}
		for _, sp := range systemPrefixes {
			if isPathUnderPrefix(rt, sp) {
				res.TargetsSystem = true
				res.AffectedPaths = append(res.AffectedPaths, rt)
				break
			}
		}
		for _, cf := range configFilePatterns {
			if strings.Contains(rt, cf) {
				res.ModifiesConfig = true
				break
			}
		}
		if isBlockDevicePath(rt) {
			res.Risk = RiskBlocked
			res.Reason = fmt.Sprintf("Redirecting output to block device: %s — destroys all data", rt)
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
	}

	for _, a := range allArgs {
		if strings.HasPrefix(a, "-") {
			continue
		}
		resolved := a
		if strings.HasPrefix(a, "~/") || a == "~" || isStreamDevicePath(a) {
			continue
		}
		for _, sp := range systemPrefixes {
			if isPathUnderPrefix(resolved, sp) {
				res.TargetsSystem = true
				res.AffectedPaths = append(res.AffectedPaths, resolved)
				break
			}
		}
	}

	if len(calls) == 0 && len(redirectTargets) > 0 {
		if res.TargetsSystem {
			res.Risk = RiskDestructive
			res.Reason = "Redirecting output to system path: " + strings.Join(res.AffectedPaths, ", ")
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskProjectWrite
		res.Reason = "Redirecting output"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	res.BaseCommand = calls[0]

	if res.Obfuscated {
		res.Risk = RiskDestructive
		res.Reason = "Command name uses shell expansion/obfuscation — possible evasion attempt"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	if credPattern.MatchString(cmd) {
		res.ReadsCredentials = true
	}

	if res.ModifiesConfig {
		res.Risk = RiskDestructive
		res.Reason = "Writing to shell or application configuration — possible persistence mechanism"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	if pipedToShell {
		if pipelineFetchesRemote(calls) {
			res.Risk = RiskBlocked
			res.Reason = "Downloading and immediately executing code from the internet — possible supply chain or prompt injection attack"
			res.Suggestion = "Download to a file first, inspect it, then execute separately"
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskDestructive
		res.Reason = "Piping command output into a shell — arbitrary code execution vector"
		res.Suggestion = "Write the code to a file and review it before executing"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}
	if prog == nil && remotePipeToShellRe.MatchString(cmd) {
		res.Risk = RiskBlocked
		res.Reason = "Downloading and immediately executing code from the internet — possible supply chain or prompt injection attack"
		res.Suggestion = "Download to a file first, inspect it, then execute separately"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	dangerousMatch := matchDangerousPattern(cmd, res)
	if dangerousMatch != nil {
		res.Risk = RiskBlocked
		res.Reason = dangerousMatch.Reason
		res.Suggestion = dangerousMatch.Suggestion
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	if containsArg(allArgs, "--no-preserve-root") {
		res.Risk = RiskBlocked
		res.Reason = "Bypassing root protection — extremely dangerous"
		res.Suggestion = "Do not use --no-preserve-root"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	// Writing into a system path is destructive whatever command produces the
	// bytes; read-only commands below only stay safe without such a redirect.
	if systemWrite := systemRedirectTargets(redirectTargets); len(systemWrite) > 0 {
		res.Risk = RiskDestructive
		res.Reason = "Redirecting output to system path: " + strings.Join(systemWrite, ", ")
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	// Every command in a list or pipeline runs, so the line is as risky as its
	// riskiest command: `cd x && rm -rf y` is a recursive delete, not a cd.
	var worst *CommandSafetyResult
	for _, call := range shellCalls {
		verdict := classifyCall(call)
		if worst == nil || riskRank(verdict.Risk) > riskRank(worst.Risk) {
			worst = verdict
		}
	}
	res.BaseCommand = worst.BaseCommand
	res.Risk, res.Reason, res.Suggestion = worst.Risk, worst.Reason, worst.Suggestion
	if res.ReadsCredentials && riskRank(res.Risk) < riskRank(RiskDestructive) {
		res.Risk = RiskDestructive
		res.Reason = "Command reads credentials or secrets"
		res.Suggestion = ""
	}
	if riskRank(res.Risk) >= riskRank(RiskDestructive) {
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
	} else {
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
	}
	return res
}

// classifyCall rates one simple command by what it runs, after unwrapping
// launchers such as env, timeout and xargs.
func classifyCall(call shellCall) *CommandSafetyResult {
	args := unwrapCommand(call.args)
	res := &CommandSafetyResult{BaseCommand: args[0]}
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "~/") || a == "~" || isStreamDevicePath(a) {
			continue
		}
		for _, sp := range systemPrefixes {
			if isPathUnderPrefix(a, sp) {
				res.TargetsSystem = true
				res.AffectedPaths = append(res.AffectedPaths, a)
				break
			}
		}
	}
	lowCmd := strings.ToLower(filepath.Base(args[0]))

	switch lowCmd {
	case "ls", "cat", "grep", "egrep", "fgrep", "rg", "ripgrep",
		"pwd", "echo", "printf", "head", "tail", "wc", "sort",
		"uniq", "diff", "comm", "cut", "tr", "od", "hexdump",
		"xxd", "nl", "fmt", "pr", "fold", "expand", "unexpand",
		"join", "paste", "which", "whoami", "who", "id", "date",
		"env", "printenv", "dirname", "basename", "readlink",
		"realpath", "file", "stat", "du", "df", "type", "time",
		"true", "false", "history", "uptime", "uname", "hostname",
		"arch", "nproc", "free", "lscpu", "lspci", "lsusb",
		"getconf", "locale", "localectl", "timedatectl",
		"dmesg", "journalctl", "help", "info", "man", "whatis",
		"apropos", "cd", "pushd", "popd", "dirs",
		"dir", "where", "findstr", "more", "less",
		"python3":
		res.Risk = RiskSafe
		return res

	case "ps", "pgrep", "sleep", "test", "[", "jq", "tree", "seq", "tac", "rev",
		"shuf", "column", "cmp", "sha256sum", "sha1sum", "md5sum", "cksum",
		"command", "hash", "tput", "tty", "lsof", "mktemp", "yes",
		"exit", "return", "set", "export", "unset", "local", "declare", "readonly",
		"read", "shift", "wait", "break", "continue", ":", "ulimit", "umask", "jobs":
		res.Risk = RiskSafe
		return res

	case "gofmt", "goimports":
		if hasShortFlag(args, 'w') {
			res.Risk = RiskProjectWrite
			res.Reason = "Formats files in place"
			return res
		}
		res.Risk = RiskSafe
		return res

	case "golangci-lint":
		if containsArg(args, "--fix") || hasArgPrefix(args, "--fix=") {
			res.Risk = RiskProjectWrite
			res.Reason = "Applies lint fixes in place"
			return res
		}
		res.Risk = RiskSafe
		return res

	case "find":
		return classifyFind(args, res)

	case "tee":
		res.Risk = RiskProjectWrite
		res.Reason = "Writes files"
		for _, a := range args[1:] {
			if !strings.HasPrefix(a, "-") && !isStreamDevicePath(a) {
				return res
			}
		}
		res.Risk = RiskSafe
		res.Reason = ""
		return res

	case "git":
		res = classifyGit(args, res)
		return res

	case "npm":
		res = classifyNpm(args, res)
		return res

	case "pip", "pip3":
		res = classifyPip(args, res)
		return res

	case "go":
		res = classifyGo(args, res)
		return res

	case "cargo":
		res = classifyCargo(args, res)
		return res

	case "rm", "rmdir", "unlink":
		res = classifyRm(args, res)
		return res

	case "mv", "cp":
		if res.TargetsSystem {
			res.Risk = RiskDestructive
			res.Reason = "Moving/copying to system path"
			return res
		}
		res.Risk = RiskProjectWrite
		res.Reason = "File move/copy"
		return res

	case "sed":
		if hasShortFlag(args, 'i') || containsArg(args, "--in-place") || hasArgPrefix(args, "--in-place=") {
			if res.TargetsSystem {
				res.Risk = RiskDestructive
				res.Reason = "In-place edit of system files"
				return res
			}
			res.Risk = RiskProjectWrite
			res.Reason = "In-place file edit"
			return res
		}
		res.Risk = RiskSafe
		return res

	case "mkdir", "touch", "ln", "rename", "install":
		if res.TargetsSystem {
			res.Risk = RiskDestructive
			res.Reason = fmt.Sprintf("Modifying system path: %s", strings.Join(res.AffectedPaths, ", "))
			return res
		}
		res.Risk = RiskProjectWrite
		return res

	case "chmod", "chown", "chattr", "setfacl":
		isRec := hasRecursiveFlag(args)
		if res.TargetsSystem && isRec {
			res.Risk = RiskBlocked
			res.Reason = fmt.Sprintf("Recursive permission change on system path: %s", strings.Join(res.AffectedPaths, ", "))
			return res
		}
		if res.TargetsSystem {
			res.Risk = RiskDestructive
			res.Reason = fmt.Sprintf("Permission change on system path: %s", strings.Join(res.AffectedPaths, ", "))
			return res
		}
		if isRec {
			res.Risk = RiskDestructive
			res.Reason = "Recursive permission change"
			return res
		}
		res.Risk = RiskProjectWrite
		return res

	case "sudo", "doas":
		res.Risk = RiskDestructive
		res.Reason = "Privilege escalation — running as root"
		return res

	case "su":
		res.Risk = RiskDestructive
		res.Reason = "Switching user"
		return res

	case "dd":
		for _, a := range args {
			if strings.HasPrefix(a, "of=") {
				target := strings.TrimPrefix(a, "of=")
				for _, sp := range systemPrefixes {
					if isPathUnderPrefix(target, sp) {
						res.Risk = RiskBlocked
						res.Reason = fmt.Sprintf("Writing directly to block device: %s", target)
						res.Suggestion = "This destroys all data on the target device"
						return res
					}
				}
				if isBlockDevicePath(target) {
					res.Risk = RiskBlocked
					res.Reason = fmt.Sprintf("Writing directly to block device: %s", target)
					res.Suggestion = "This destroys all data on the target device"
					return res
				}
			}
		}
		res.Risk = RiskDestructive
		res.Reason = "Direct disk write — can destroy data"
		return res

	case "mkfs", "mkfs.ext4", "mkfs.xfs", "mkfs.btrfs",
		"fdisk", "parted", "gdisk", "sfdisk",
		"mkswap", "swapon", "swapoff":
		res.Risk = RiskBlocked
		res.Reason = "Disk partitioning/formatting — can destroy all data on a drive"
		res.Suggestion = "This should not be done by an AI agent. If needed, do it manually."
		return res

	case "shred", "wipe", "wipefs", "blkdiscard":
		res.Risk = RiskBlocked
		res.Reason = "Irreversible data destruction"
		res.Suggestion = "This cannot be undone. Do not run this from an AI agent."
		return res

	case "reboot", "shutdown", "poweroff", "halt", "init":
		res.Risk = RiskDestructive
		res.Reason = "System state change"
		return res

	case "apt", "apt-get", "aptitude", "dpkg", "rpm",
		"yum", "dnf", "zypper", "pacman":
		if containsFlag(args, "--allow-unauthenticated") {
			res.Risk = RiskBlocked
			res.Reason = "Installing packages without authentication check — security risk"
			return res
		}
		res.Risk = RiskDestructive
		res.Reason = "Package manager — modifies system packages"
		return res

	case "curl", "wget":
		res.Risk = RiskNetwork
		res.Reason = "Network request"
		return res

	case "ssh", "scp", "sftp", "rsync":
		res.Risk = RiskNetwork
		res.Reason = "Remote connection — network access"
		return res

	case "nc", "ncat", "netcat", "socat", "telnet", "ftp", "ping":
		res.Risk = RiskNetwork
		res.Reason = "Network tool — external connection"
		return res

	case "docker", "podman", "nerdctl":
		if len(args) == 2 && (args[1] == "--version" || args[1] == "version" || args[1] == "info") ||
			hasAnyArg(args, "ps", "images", "inspect", "logs") {
			res.Risk = RiskSafe
			return res
		}
		res.Risk = RiskDestructive
		res.Reason = "Container operation with potential system impact"
		return res

	case "eval", "source", ".":
		res.Risk = RiskDestructive
		res.Reason = "Shell code evaluation — can execute arbitrary code"
		return res

	case "kill", "pkill", "killall":
		res.Risk = RiskProjectWrite
		res.Reason = "Process termination"
		return res

	case "passwd", "chpasswd", "adduser", "useradd",
		"usermod", "userdel", "groupadd", "groupmod", "groupdel":
		res.Risk = RiskBlocked
		res.Reason = "User/group management — should not be done by an AI agent"
		res.Suggestion = "Manage users manually or use configuration management tools"
		return res

	case "crontab", "at", "batch":
		res.Risk = RiskDestructive
		res.Reason = "Scheduling commands — possible persistence mechanism"
		return res

	case "systemctl", "service":
		if hasAnyArg(args, "enable", "disable", "start", "stop", "restart") {
			res.Risk = RiskDestructive
			res.Reason = "System service modification"
			return res
		}
		res.Risk = RiskSafe
		return res
	}

	if res.TargetsSystem {
		res.Risk = RiskDestructive
		res.Reason = fmt.Sprintf("Command targets system files: %s", strings.Join(res.AffectedPaths, ", "))
		return res
	}

	res.Risk = RiskUnknown
	res.Reason = "Unknown command — requires confirmation"
	return res
}

func systemRedirectTargets(targets []string) []string {
	var out []string
	for _, rt := range targets {
		if isStreamDevicePath(rt) {
			continue
		}
		for _, sp := range systemPrefixes {
			if isPathUnderPrefix(rt, sp) {
				out = append(out, rt)
				break
			}
		}
	}
	return out
}

// shellCall is one simple command: args[0] is the command name.
type shellCall struct {
	args []string
}

// launcherValueFlags lists, per launcher, the options that consume the next
// argument, so the launched command is found after them.
var launcherValueFlags = map[string]map[string]bool{
	"env":     {"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true, "--split-string": true},
	"timeout": {"-s": true, "--signal": true, "-k": true, "--kill-after": true},
	"nice":    {"-n": true, "--adjustment": true},
	"ionice":  {"-c": true, "--class": true, "-n": true, "--classdata": true},
	"stdbuf":  {"-i": true, "-o": true, "-e": true},
	"xargs": {"-I": true, "-n": true, "--max-args": true, "-P": true, "--max-procs": true, "-L": true,
		"--max-lines": true, "-d": true, "--delimiter": true, "-s": true, "--max-chars": true,
		"-a": true, "--arg-file": true, "-E": true},
	"time": {}, "nohup": {}, "setsid": {}, "exec": {}, "command": {},
}

// unwrapCommand returns the command a launcher runs (`env X=1 go test`,
// `timeout 60 go test`, `xargs rm`). A launcher with nothing to launch, or
// `command -v` lookups, is returned unchanged.
func unwrapCommand(args []string) []string {
	for len(args) > 1 {
		name := strings.ToLower(filepath.Base(args[0]))
		valueFlags, ok := launcherValueFlags[name]
		if !ok {
			return args
		}
		if name == "command" && (containsArg(args, "-v") || containsArg(args, "-V")) {
			return args
		}
		i := 1
		for i < len(args) {
			a := args[i]
			switch {
			case a == "--":
				i++
			case strings.HasPrefix(a, "-") && len(a) > 1:
				i++
				if valueFlags[a] && i < len(args) {
					i++
				}
				continue
			case name == "env" && strings.Contains(a, "="):
				i++
				continue
			case name == "timeout" && a != "" && (a[0] >= '0' && a[0] <= '9'):
				i++
			}
			break
		}
		if i >= len(args) {
			return args
		}
		args = args[i:]
	}
	return args
}

// hasShortFlag reports whether a single-dash option cluster (`-n`, `-ni`,
// `-i.bak`) sets the given letter. Long options and operands never match.
func hasShortFlag(args []string, letter byte) bool {
	for _, a := range args[1:] {
		if len(a) < 2 || a[0] != '-' || a[1] == '-' {
			continue
		}
		if strings.IndexByte(a[1:], letter) >= 0 {
			return true
		}
	}
	return false
}

func collectAllArgs(stmts []*syntax.CallExpr) []string {
	var all []string
	for _, stmt := range stmts {
		for _, w := range stmt.Args {
			all = append(all, resolveWord(w))
		}
	}
	return all
}

func hasRecursiveFlag(args []string) bool {
	return containsArg(args, "--recursive") || hasShortFlag(args, 'r') || hasShortFlag(args, 'R')
}

func hasArgPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func containsArg(args []string, arg string) bool {
	for _, a := range args {
		if a == arg {
			return true
		}
	}
	return false
}

func hasAnyArg(args []string, targets ...string) bool {
	for _, a := range args {
		for _, t := range targets {
			if a == t {
				return true
			}
		}
	}
	return false
}

// classifyFind treats find as a read unless it deletes, writes a listing to
// a file, or runs commands; an -exec command is judged like any other.
func classifyFind(args []string, res *CommandSafetyResult) *CommandSafetyResult {
	worst := &CommandSafetyResult{BaseCommand: args[0], Risk: RiskSafe}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-delete":
			worst = &CommandSafetyResult{BaseCommand: args[0], Risk: RiskDestructive, Reason: "find -delete removes every match"}
		case "-fprint", "-fprint0", "-fprintf", "-fls":
			if riskRank(worst.Risk) < riskRank(RiskProjectWrite) {
				worst = &CommandSafetyResult{BaseCommand: args[0], Risk: RiskProjectWrite, Reason: "find writes a listing file"}
			}
		case "-exec", "-execdir", "-ok", "-okdir":
			end := i + 1
			for end < len(args) && args[end] != ";" && args[end] != "+" {
				end++
			}
			if end > i+1 {
				verdict := classifyCall(shellCall{args: args[i+1 : end]})
				if riskRank(verdict.Risk) > riskRank(worst.Risk) {
					worst = verdict
				}
			}
			i = end
		}
	}
	res.Risk, res.Reason = worst.Risk, worst.Reason
	if worst.BaseCommand != args[0] {
		res.BaseCommand = worst.BaseCommand
	}
	return res
}

func classifyGit(args []string, res *CommandSafetyResult) *CommandSafetyResult {
	subCmd := ""
	for _, a := range args {
		switch a {
		case "push", "fetch", "pull", "clone":
			if subCmd != "" {
				break
			}
			subCmd = a
		case "add", "rm", "mv", "commit", "rebase",
			"reset", "cherry-pick", "merge", "clean",
			"checkout", "switch", "stash", "bisect":
			if subCmd == "" {
				subCmd = a
			}
		}
	}

	switch subCmd {
	case "push", "fetch", "pull", "clone":
		for _, a := range args {
			if a == "--force" || a == "--force-with-lease" || a == "-f" {
				res.Risk = RiskDestructive
				res.Reason = "Force push can overwrite remote history"
				return res
			}
		}
		res.Risk = RiskNetwork
		res.Reason = "Remote git operation"
		return res

	case "reset":
		for _, a := range args {
			if a == "--hard" || a == "--merge" || a == "--keep" {
				res.Risk = RiskDestructive
				res.Reason = fmt.Sprintf("Git reset %s discards uncommitted changes irreversibly", a)
				return res
			}
		}
		res.Risk = RiskProjectWrite
		res.Reason = "Git write operation"
		return res

	case "clean":
		for _, a := range args {
			if a == "-n" || a == "--dry-run" {
				res.Risk = RiskSafe
				res.Reason = "Git clean dry run"
				return res
			}
		}
		res.Risk = RiskDestructive
		res.Reason = "Git clean removes untracked files — changes cannot be recovered"
		return res

	case "checkout":
		for _, a := range args {
			if strings.HasPrefix(a, "-") && strings.ContainsAny(a, "f") {
				res.Risk = RiskDestructive
				res.Reason = "Force checkout discards local changes"
				return res
			}
		}
		res.Risk = RiskProjectWrite
		res.Reason = "Git write operation"
		return res

	case "add", "rm", "mv", "commit", "rebase", "cherry-pick", "merge",
		"switch", "stash", "bisect":
		res.Risk = RiskProjectWrite
		res.Reason = "Git write operation"
		return res
	}

	res.Risk = RiskSafe
	return res
}

func classifyNpm(args []string, res *CommandSafetyResult) *CommandSafetyResult {
	for _, a := range args {
		switch a {
		case "install", "i", "add", "uninstall", "remove", "update", "upgrade":
			res.Risk = RiskNetwork
			res.Reason = "Installing/updating npm packages"
			return res
		}
	}
	res.Risk = RiskSafe
	return res
}

func classifyPip(args []string, res *CommandSafetyResult) *CommandSafetyResult {
	for _, a := range args {
		switch a {
		case "install", "uninstall", "download", "wheel":
			res.Risk = RiskNetwork
			res.Reason = "Python package operation"
			return res
		}
	}
	res.Risk = RiskSafe
	return res
}

func classifyGo(args []string, res *CommandSafetyResult) *CommandSafetyResult {
	for _, a := range args {
		switch a {
		case "install", "get", "mod":
			res.Risk = RiskNetwork
			res.Reason = "Go package/module operation"
			return res
		}
	}
	res.Risk = RiskSafe
	return res
}

func classifyCargo(args []string, res *CommandSafetyResult) *CommandSafetyResult {
	for _, a := range args {
		switch a {
		case "install", "uninstall", "publish":
			res.Risk = RiskNetwork
			res.Reason = "Cargo package operation"
			return res
		}
	}
	res.Risk = RiskSafe
	return res
}

func classifyRm(args []string, res *CommandSafetyResult) *CommandSafetyResult {
	isRec := hasRecursiveFlag(args)
	if res.TargetsSystem && isRec {
		res.Risk = RiskBlocked
		res.Reason = fmt.Sprintf("Recursive deletion of system paths: %s", strings.Join(res.AffectedPaths, ", "))
		res.Suggestion = "Target specific files instead of using -r on system paths"
		return res
	}
	if isRec {
		res.Risk = RiskDestructive
		res.Reason = "Recursive deletion — can remove many files at once"
		return res
	}
	if res.TargetsSystem {
		res.Risk = RiskDestructive
		res.Reason = "Deleting system files"
		return res
	}
	res.Risk = RiskProjectWrite
	res.Reason = "File deletion"
	return res
}
