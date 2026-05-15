package safety

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"crude/logger"
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
	Risk              CommandRisk
	BaseCommand       string
	FullCommand       string
	Reason            string
	Suggestion        string
	TargetsSystem     bool
	UsesSudo          bool
	PipedToShell      bool
	ModifiesConfig    bool
	ReadsCredentials  bool
	AffectedPaths     []string
	RedirectTargets   []string
	Calls             []string
}

var systemPrefixes = []string{
	"/", "/etc", "/dev", "/proc", "/sys", "/boot", "/usr", "/bin",
	"/sbin", "/lib", "/lib64", "/opt", "/root", "/var/log",
	"/var/lib", "/var/spool",
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

// wordString reconstructs the full string of a Word using the printer.
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

// extractCmds walks a parsed shell AST and returns structured info about all commands.
func extractCmds(prog *syntax.File) (calls []string, pipedToShell bool, redirectTargets []string, stmts []*syntax.CallExpr) {
	pipeChain := false
	lastInPipe := false

	syntax.Walk(prog, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.BinaryCmd:
			pipeChain = (n.Op == syntax.AndStmt || n.Op == syntax.OrStmt || n.Op == syntax.PipeAll || n.Op == syntax.Pipe)
		case *syntax.Stmt:
			lastInPipe = pipeChain && n.Cmd != nil
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				return true
			}
			cmdName := wordString(n.Args[0])
			calls = append(calls, cmdName)
			stmts = append(stmts, n)
			if lastInPipe && (cmdName == "sh" || cmdName == "bash" || cmdName == "zsh" || cmdName == "dash" || cmdName == "fish" || cmdName == "source") {
				pipedToShell = true
			}
		case *syntax.Redirect:
			target := wordString(n.Word)
			if target != "" {
				redirectTargets = append(redirectTargets, target)
			}
		}
		return true
	})
	return
}

// CheckCommandSafety analyzes a shell command and returns its risk assessment.
func CheckCommandSafety(cmd string, lg *logger.Logger) *CommandSafetyResult {
	res := &CommandSafetyResult{
		FullCommand: cmd,
	}

	if strings.TrimSpace(cmd) == "" {
		res.Risk = RiskBlocked
		res.Reason = "Empty command"
		return res
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	prog, err := parser.Parse(strings.NewReader(cmd), "")
	if err != nil {
		res.Risk = RiskDestructive
		res.Reason = fmt.Sprintf("Could not parse command — treating as dangerous: %v", err)
		lg.Debug("COMMAND", "parse", "error", err.Error(), "risk", res.Risk.String())
		return res
	}

	calls, pipedToShell, redirectTargets, stmts := extractCmds(prog)
	res.Calls = calls
	res.PipedToShell = pipedToShell
	res.RedirectTargets = redirectTargets

	for _, rt := range redirectTargets {
		for _, sp := range systemPrefixes {
			if strings.HasPrefix(rt, sp) {
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
		if strings.HasPrefix(rt, "/dev/sd") || strings.HasPrefix(rt, "/dev/nvme") || strings.HasPrefix(rt, "/dev/mmcblk") || strings.HasPrefix(rt, "/dev/loop") {
			res.Risk = RiskBlocked
			res.Reason = fmt.Sprintf("Redirecting output to block device: %s — destroys all data", rt)
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
	}

	// Check all CallExpr args for system paths and config file targets too
	allArgs := collectAllArgs(stmts)
	for _, a := range allArgs {
		if strings.HasPrefix(a, "-") {
			continue
		}
		resolved := a
		if strings.HasPrefix(a, "~/") || a == "~" {
			continue
		}
		for _, sp := range systemPrefixes {
			if strings.HasPrefix(resolved, sp) {
				res.TargetsSystem = true
				res.AffectedPaths = append(res.AffectedPaths, resolved)
				break
			}
		}
	}

	if len(calls) == 0 && len(redirectTargets) > 0 {
		if res.TargetsSystem {
			res.Risk = RiskDestructive
			res.Reason = fmt.Sprintf("Redirecting output to system path: %s", strings.Join(res.AffectedPaths, ", "))
			lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskProjectWrite
		res.Reason = "Redirecting output"
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	firstCmd := calls[0]
	res.BaseCommand = firstCmd
	argsAfterFirst := allArgs

	if credPattern.MatchString(cmd) {
		res.ReadsCredentials = true
	}

	if res.ModifiesConfig {
		res.Risk = RiskDestructive
		res.Reason = "Writing to shell or application configuration — possible persistence mechanism"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	if pipedToShell || regexp.MustCompile(`(?i)(curl|wget)\s+.*\|\s*(ba|sh|zsh|bash|dash|fish|source)\b`).MatchString(cmd) {
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
		"python --version", "go version", "node --version",
		"npm --version", "rustc --version", "cargo --version",
		"pip --version":
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
			lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		res.Risk = RiskProjectWrite
		res.Reason = "File move/copy"
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res

	case "sed":
		if hasFlag(argsAfterFirst, "i") || hasFlag(argsAfterFirst, "i") {
			if res.TargetsSystem {
				res.Risk = RiskDestructive
				res.Reason = "In-place edit of system files"
				lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
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
			lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
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
			lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
		if isRec {
			res.Risk = RiskDestructive
			res.Reason = "Recursive permission change"
			lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
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
					if strings.HasPrefix(target, sp) {
						res.Risk = RiskBlocked
						res.Reason = fmt.Sprintf("Writing directly to block device: %s", target)
						res.Suggestion = "This destroys all data on the target device"
						lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
						return res
					}
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
		if pipedToShell || isPipedToShell(prog) {
			res.Risk = RiskBlocked
			res.Reason = "Downloading and immediately executing code — possible supply chain or prompt injection attack"
			res.Suggestion = "Download to a file first, inspect it, then execute separately"
			lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
			return res
		}
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

	// Check for heredoc with eval/source
	if isHeredocEval(prog) {
		res.Risk = RiskDestructive
		res.Reason = "Evaluating code from heredoc — possible arbitrary code execution"
		lg.Warn("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
		return res
	}

	if res.PipedToShell || (len(calls) > 1 && (calls[len(calls)-1] == "sh" || calls[len(calls)-1] == "bash")) {
		res.Risk = RiskBlocked
		res.Reason = "Piping command output to a shell for execution — potential injection vector"
		res.Suggestion = "Avoid piping to shell. Use explicit commands instead."
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
		lg.Debug("COMMAND", "risk", "risk", res.Risk.String(), "base", res.BaseCommand, "reason", res.Reason)
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
			all = append(all, wordString(w))
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

func isPipedToShell(prog *syntax.File) bool {
	var lastCmd string
	var isPiped bool
	syntax.Walk(prog, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				isPiped = true
			} else {
				isPiped = false
			}
		case *syntax.CallExpr:
			if len(n.Args) > 0 {
				lastCmd = wordString(n.Args[0])
			}
		}
		return true
	})
	return isPiped && (lastCmd == "sh" || lastCmd == "bash" || lastCmd == "zsh" || lastCmd == "dash" || lastCmd == "fish" || lastCmd == "source")
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
				firstCmd = wordString(n.Args[0])
			}
		}
		return true
	})
	return hasHeredoc && (firstCmd == "eval" || firstCmd == "source" || firstCmd == ".")
}

func classifyGit(args []string, res *CommandSafetyResult) *CommandSafetyResult {
	for _, a := range args {
		switch a {
		case "push", "fetch", "pull", "clone":
			res.Risk = RiskNetwork
			res.Reason = "Remote git operation"
			return res
		case "add", "rm", "mv", "commit", "rebase",
			"reset", "cherry-pick", "merge":
			res.Risk = RiskProjectWrite
			res.Reason = "Git write operation"
			return res
		}
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
