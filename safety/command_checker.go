package safety

import (
	"bytes"
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
			// Match against POSIX-style prefixes only — Windows drive
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

var expandCfg = &expand.Config{
	Env: expand.FuncEnviron(os.Getenv),
}

func preprocessCommand(cmd string) string {
	normalized := cmd
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	for cdPrefixRe.MatchString(normalized) {
		normalized = cdPrefixRe.ReplaceAllString(normalized, "")
	}
	normalized = cmdPrefixRe.ReplaceAllString(normalized, "")
	return normalized
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
			target := resolveWord(n.Word)
			if target != "" {
				redirectTargets = append(redirectTargets, target)
			}
		}
		return true
	})
	return
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
	var prog *syntax.File

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	parsedProg, parseErr := parser.Parse(strings.NewReader(preprocessed), "")
	if parseErr == nil {
		var stmts []*syntax.CallExpr
		var obfuscated bool
		calls, pipedToShell, redirectTargets, stmts, obfuscated = extractCmds(parsedProg)
		res.Obfuscated = obfuscated
		allArgs = collectAllArgs(stmts)
		prog = parsedProg
	} else {
		calls, pipedToShell, redirectTargets, allArgs = extractCmdsSimple(preprocessed)
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
		if strings.HasPrefix(a, "~/") || a == "~" {
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

	firstCmd := calls[0]
	res.BaseCommand = firstCmd
	argsAfterFirst := allArgs

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

	lowCmd := strings.ToLower(firstCmd)

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
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "git":
		res = classifyGit(argsAfterFirst, res)
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "npm":
		res = classifyNpm(argsAfterFirst, res)
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "pip", "pip3":
		res = classifyPip(argsAfterFirst, res)
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "go":
		res = classifyGo(argsAfterFirst, res)
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "cargo":
		res = classifyCargo(argsAfterFirst, res)
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "rm", "rmdir", "unlink":
		res = classifyRm(argsAfterFirst, res)
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "mv", "cp":
		if res.TargetsSystem {
			res.Risk = RiskDestructive
			res.Reason = "Moving/copying to system path"
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskProjectWrite
		res.Reason = "File move/copy"
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "sed":
		if hasFlag(argsAfterFirst, "i") {
			if res.TargetsSystem {
				res.Risk = RiskDestructive
				res.Reason = "In-place edit of system files"
				lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
				return res
			}
			res.Risk = RiskProjectWrite
			res.Reason = "In-place file edit"
			lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskSafe
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "mkdir", "touch", "ln", "rename", "install":
		if res.TargetsSystem {
			res.Risk = RiskDestructive
			res.Reason = fmt.Sprintf("Modifying system path: %s", strings.Join(res.AffectedPaths, ", "))
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskProjectWrite
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "chmod", "chown", "chattr", "setfacl":
		isRec := hasRecursiveFlag(allArgs)
		if res.TargetsSystem && isRec {
			res.Risk = RiskBlocked
			res.Reason = fmt.Sprintf("Recursive permission change on system path: %s", strings.Join(res.AffectedPaths, ", "))
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		if res.TargetsSystem {
			res.Risk = RiskDestructive
			res.Reason = fmt.Sprintf("Permission change on system path: %s", strings.Join(res.AffectedPaths, ", "))
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		if isRec {
			res.Risk = RiskDestructive
			res.Reason = "Recursive permission change"
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskProjectWrite
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "sudo", "doas":
		res.Risk = RiskDestructive
		res.Reason = "Privilege escalation — running as root"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "su":
		res.Risk = RiskDestructive
		res.Reason = "Switching user"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "dd":
		for _, a := range allArgs {
			if strings.HasPrefix(a, "of=") {
				target := strings.TrimPrefix(a, "of=")
				for _, sp := range systemPrefixes {
					if isPathUnderPrefix(target, sp) {
						res.Risk = RiskBlocked
						res.Reason = fmt.Sprintf("Writing directly to block device: %s", target)
						res.Suggestion = "This destroys all data on the target device"
						lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
						return res
					}
				}
				if isBlockDevicePath(target) {
					res.Risk = RiskBlocked
					res.Reason = fmt.Sprintf("Writing directly to block device: %s", target)
					res.Suggestion = "This destroys all data on the target device"
					lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
					return res
				}
			}
		}
		res.Risk = RiskDestructive
		res.Reason = "Direct disk write — can destroy data"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "mkfs", "mkfs.ext4", "mkfs.xfs", "mkfs.btrfs",
		"fdisk", "parted", "gdisk", "sfdisk",
		"mkswap", "swapon", "swapoff":
		res.Risk = RiskBlocked
		res.Reason = "Disk partitioning/formatting — can destroy all data on a drive"
		res.Suggestion = "This should not be done by an AI agent. If needed, do it manually."
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "shred", "wipe", "wipefs", "blkdiscard":
		res.Risk = RiskBlocked
		res.Reason = "Irreversible data destruction"
		res.Suggestion = "This cannot be undone. Do not run this from an AI agent."
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "reboot", "shutdown", "poweroff", "halt", "init":
		res.Risk = RiskDestructive
		res.Reason = "System state change"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "apt", "apt-get", "aptitude", "dpkg", "rpm",
		"yum", "dnf", "zypper", "pacman":
		if containsFlag(allArgs, "--allow-unauthenticated") {
			res.Risk = RiskBlocked
			res.Reason = "Installing packages without authentication check — security risk"
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskDestructive
		res.Reason = "Package manager — modifies system packages"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "curl", "wget":
		res.Risk = RiskNetwork
		res.Reason = "Network request"
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "ssh", "scp", "sftp", "rsync":
		res.Risk = RiskNetwork
		res.Reason = "Remote connection — network access"
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "nc", "ncat", "netcat", "socat", "telnet", "ftp", "ping":
		res.Risk = RiskNetwork
		res.Reason = "Network tool — external connection"
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "docker", "podman", "nerdctl":
		if hasAnyArg(allArgs, "run", "exec", "build", "push", "pull") {
			res.Risk = RiskDestructive
			res.Reason = "Container operation with potential system impact"
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskDestructive
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "eval", "source", ".":
		res.Risk = RiskDestructive
		res.Reason = "Shell code evaluation — can execute arbitrary code"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "kill", "pkill", "killall":
		res.Risk = RiskProjectWrite
		res.Reason = "Process termination"
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "passwd", "chpasswd", "adduser", "useradd",
		"usermod", "userdel", "groupadd", "groupmod", "groupdel":
		res.Risk = RiskBlocked
		res.Reason = "User/group management — should not be done by an AI agent"
		res.Suggestion = "Manage users manually or use configuration management tools"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "crontab", "at", "batch":
		res.Risk = RiskDestructive
		res.Reason = "Scheduling commands — possible persistence mechanism"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "systemctl", "service":
		if hasAnyArg(allArgs, "enable", "disable", "start", "stop", "restart") {
			res.Risk = RiskDestructive
			res.Reason = "System service modification"
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskSafe
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	if prog != nil && isHeredocEval(prog) {
		res.Risk = RiskDestructive
		res.Reason = "Evaluating code from heredoc — possible arbitrary code execution"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	if res.ReadsCredentials {
		res.Risk = RiskDestructive
		res.Reason = "Command reads credentials or secrets"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	if res.TargetsSystem {
		res.Risk = RiskDestructive
		res.Reason = fmt.Sprintf("Command targets system files: %s", strings.Join(res.AffectedPaths, ", "))
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	res.Risk = RiskUnknown
	res.Reason = "Unknown command — requires confirmation"
	lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
	return res
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

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		a = strings.TrimLeft(a, "-")
		if strings.Contains(a, flag) {
			return true
		}
	}
	return false
}

func hasRecursiveFlag(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		a = strings.ToLower(a)
		a = strings.TrimLeft(a, "-")
		if a == "recursive" || strings.Contains(a, "r") {
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

func isHeredocEval(prog *syntax.File) bool {
	hasHeredoc := false
	var firstCmd string
	syntax.Walk(prog, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Redirect:
			if n.Hdoc != nil {
				hasHeredoc = true
			}
		case *syntax.CallExpr:
			if len(n.Args) > 0 && firstCmd == "" {
				firstCmd = resolveWord(n.Args[0])
			}
		}
		return true
	})
	return hasHeredoc && (firstCmd == "eval" || firstCmd == "source" || firstCmd == ".")
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
