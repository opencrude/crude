package session

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"crude/agent/codexio"
	"crude/agent/safety"
	"crude/agent/tools"
	"crude/agent/turnloop"
	"crude/agent/worktree"
	"crude/internal/gitplatform"
	"crude/types/command"
	"crude/types/toolresult"
	"crude/workspace/fileio"

	"github.com/mattn/go-shellwords"
)

func planInvestigationFileTool(tool string) bool {
	switch tool {
	case "apply_patch", "write_file", "edit_file", "move_file", "copy_file":
		return true
	}
	return false
}

func planInvestigationCommandTool(tool string) bool {
	switch tool {
	case "execute_command", "async_execute_command", "exec_command":
		return true
	}
	return false
}

func investigationDenied(err error) (tools.ToolResult, error) {
	return tools.ToolResult{Outcome: toolresult.Outcome{State: toolresult.Denied}}, err
}

func (s *Session) investigationCheckout(ctx context.Context, sourceID string, owner worktree.Owner) (worktree.Context, error) {
	checkout, err := s.activePlanInvestigation(ctx, sourceID, owner)
	if errors.Is(err, errPlanInvestigationNotOpen) {
		err = errors.New("open an investigation checkout first. No changes or commands ran on the source")
	}
	if err == nil && (checkout.ID == "" || !filepath.IsAbs(checkout.Root) || !checkout.Ready || checkout.Removed ||
		checkout.Investigation == nil || checkout.Investigation.SessionID != s.sessionID ||
		checkout.Investigation.Cycle != planInvestigationCycleFromContext(ctx) || checkout.SourceContextID != sourceID) {
		err = errors.New("the investigation checkout is not available for this turn")
	}
	return checkout, err
}

func (s *Session) executePlanInvestigationTool(ctx context.Context, sourceID, sourceRoot string, owner worktree.Owner, generation uint64, tool string, params map[string]any) (tools.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Outcome: toolresult.Outcome{State: toolresult.Cancelled}}, err
	}
	switch {
	case tool == "open_plan_investigation":
		if len(params) != 0 {
			return investigationDenied(errors.New("open investigation does not take arguments"))
		}
		c, err := s.openPlanInvestigation(ctx, sourceID, owner)
		if err != nil {
			return investigationDenied(err)
		}
		return tools.ToolResult{Content: fmt.Sprintf("Investigation checkout: %s\nUse this absolute folder to read experiment files. Relative paths for edits and patches use this checkout, not the source.", c.Root)}, nil
	case planInvestigationFileTool(tool):
		if !s.planInvestigationToolMu.TryLock() {
			return investigationDenied(errors.New("an investigation operation is still running. Wait for it to finish before making changes"))
		}
		defer s.planInvestigationToolMu.Unlock()
		c, err := s.investigationCheckout(ctx, sourceID, owner)
		if err != nil {
			return investigationDenied(err)
		}
		return s.mutatePlanInvestigation(ctx, c.Root, tool, params)
	case tool == "write_stdin":
		switch getStrParam(params, "chars") {
		case "":
			return executeCodexCommand(ctx, s.asyncMgr, sourceRoot, tool, params, s.commandPolicy(tool, params))
		case "\x03":
			// Do not inject a byte into a process's input. An ordinary pipe
			// can treat that byte as code, rather than a terminal interrupt.
			id := tools.IntArgAny(params, "session_id", 0)
			info, ok := s.asyncMgr.Check(fmt.Sprintf("auto-%d", id))
			if !ok {
				return investigationDenied(errors.New("the command session was not found"))
			}
			checkout, err := s.investigationCheckout(ctx, sourceID, owner)
			if err != nil {
				return investigationDenied(err)
			}
			if _, err := scopedInvestigationPath(checkout.Root, info.CWD, true); err != nil {
				return investigationDenied(errors.New("this command does not belong to the investigation checkout"))
			}
			if err := s.asyncMgr.KillRef(info.Ref()); err != nil {
				return investigationDenied(err)
			}
			return tools.ToolResult{Content: "Command stopped."}, nil
		default:
			return investigationDenied(errors.New("not allowed in Plan mode: interactive input to commands"))
		}
	case planInvestigationCommandTool(tool):
		return s.executePlanInvestigationCommand(ctx, sourceID, sourceRoot, owner, generation, tool, params)
	}
	return investigationDenied(fmt.Errorf("not allowed in Plan mode: %s", tool))
}

// Resolve all operands before any native operation. Verify them again under
// the Editor mutation lease using its actual prepared mutation receipt.
func (s *Session) mutatePlanInvestigation(ctx context.Context, root, tool string, params map[string]any) (tools.ToolResult, error) {
	resolved := maps.Clone(params)
	if resolved == nil {
		resolved = make(map[string]any)
	}
	if tool == "apply_patch" {
		input := tools.StrArg(params, "input", "")
		if err := turnloop.CheckPatchPolicy(turnloop.PlanInvestigationPatchPolicy(), input); err != nil {
			return investigationDenied(err)
		}
		patch, err := codexio.Parse(input)
		if err != nil {
			return investigationDenied(err)
		}
		for _, op := range patch.Operations {
			if _, err := scopedInvestigationPath(root, op.Path, false); err != nil {
				return investigationDenied(err)
			}
			if op.MoveTo != "" {
				if _, err := scopedInvestigationPath(root, op.MoveTo, false); err != nil {
					return investigationDenied(err)
				}
			}
		}
	} else {
		keys := []string{"path"}
		if tool == "move_file" || tool == "copy_file" {
			keys = []string{"source", "destination"}
		}
		for _, key := range keys {
			path, err := scopedInvestigationPath(root, tools.StrArg(params, key, ""), false)
			if err != nil {
				return investigationDenied(err)
			}
			resolved[key] = path
		}
	}
	opts := &tools.ToolOptions{WorkDir: root, PrepareMutation: func(receipt fileio.MutationReceipt) (fileio.MutationComplete, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, state := range receipt.Paths {
			if _, err := scopedInvestigationPath(root, state.Path, false); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}}
	result := tools.ExecuteWithJSONOptions(tool, resolved, s.exec, s.editor, false, s.logger, ctx, opts)
	if turnloop.IsToolError(result.Content) {
		return result, errors.New(result.Content)
	}
	return result, nil
}

// The worktree root is owner-supplied and canonical. No Git administration
// path, alias, symlink or junction is a valid target inside it. Directory
// mode is used for existing working directories, not file mutations.
func scopedInvestigationPath(root, value string, directory bool) (string, error) {
	if value == "" || !filepath.IsAbs(root) || strings.ContainsRune(value, 0) {
		return "", errors.New("an investigation path is required")
	}
	path := value
	if !filepath.IsAbs(path) {
		if !filepath.IsLocal(path) {
			return "", fmt.Errorf("path %q is outside the investigation checkout", value)
		}
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) || (rel == "." && !directory) {
		return "", fmt.Errorf("path %q is outside the investigation checkout", value)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil || !sameToolPath(canonicalRoot, root) {
		return "", errors.New("the investigation checkout moved or is not available")
	}
	if rel == "." {
		info, err := os.Lstat(root)
		if err != nil || !info.IsDir() {
			return "", errors.New("the investigation checkout is not a folder")
		}
		return path, nil
	}
	at := root
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		if strings.EqualFold(strings.TrimRight(part, " ."), ".git") || (runtime.GOOS == "windows" &&
			(strings.ContainsAny(part, ":~") || part != strings.TrimRight(part, " ."))) {
			return "", fmt.Errorf("path %q uses Git data or an unsafe file name", value)
		}
		at = filepath.Join(at, part)
		info, statErr := os.Lstat(at)
		if !directory && errors.Is(statErr, os.ErrNotExist) {
			// Continue checking the names of missing descendants as well.
			continue
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || (directory || i < len(parts)-1) && !info.IsDir() {
			return "", fmt.Errorf("path %q contains a link or a folder that is not available", value)
		}
	}
	return path, nil
}

func sameToolPath(left, right string) bool {
	rel, err := filepath.Rel(left, right)
	return err == nil && rel == "."
}

type planCommandRoute struct {
	line, cwd, cwdKey string
	inspect           *fileio.CommandOptions
	checkout          worktree.Context
	consent           string
}

func (s *Session) routePlanCommand(ctx context.Context, sourceID, sourceRoot string, owner worktree.Owner, tool string, params map[string]any) (planCommandRoute, error) {
	cmdKey, cwdKey := "command", "cwd"
	if tool == "exec_command" {
		cmdKey, cwdKey = "cmd", "workdir"
	}
	line, dir := tools.StrArg(params, cmdKey, ""), tools.StrArg(params, cwdKey, "")
	route := planCommandRoute{line: line, cwdKey: cwdKey}
	if line == "" {
		return route, errors.New("supply a command to run")
	}
	if tools.StrArg(params, "sandbox_permissions", "") == "require_escalated" {
		return route, errors.New("not allowed in Plan mode: elevated command permissions")
	}
	policy := s.commandPolicy(tool, params)
	if check := safety.CheckCommandSafetyWithPolicy(line, s.logger, policy); check.Risk.IsAlwaysBlocked() {
		return route, fmt.Errorf("command blocked: %s", check.Reason)
	}
	if sourceCanonical, err := filepath.EvalSymlinks(sourceRoot); err == nil &&
		(dir == "" || pathInside(sourceRoot, absoluteToolPath(sourceRoot, dir))) {
		sourceDir := absoluteToolPath(sourceRoot, dir)
		resolved, err := filepath.EvalSymlinks(sourceDir)
		if err == nil && pathInside(sourceCanonical, resolved) {
			if inspection := planInspection(ctx, line, sourceCanonical, resolved); inspection != nil {
				route.cwd, route.inspect = resolved, inspection
				return route, nil
			}
		}
	}
	c, err := s.investigationCheckout(ctx, sourceID, owner)
	if err != nil {
		return route, err
	}
	if dir == "" {
		dir = c.Root
	}
	cwd, err := scopedInvestigationPath(c.Root, dir, true)
	if err != nil {
		return route, err
	}
	consent, err := s.investigationConsent(c.ID)
	if err != nil {
		return route, err
	}
	if consent == "deny" {
		return route, errors.New("the user did not allow commands for this investigation")
	}
	route.checkout, route.cwd, route.consent = c, cwd, consent
	return route, nil
}

func absoluteToolPath(root, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(root, value)
}

func (s *Session) planInvestigationHostRequest(ctx context.Context, sourceID, sourceRoot string, owner worktree.Owner, tool string, params map[string]any) (*turnloop.PlanInvestigationHostRequest, error) {
	route, err := s.routePlanCommand(ctx, sourceID, sourceRoot, owner, tool, params)
	if err != nil || route.inspect != nil || route.consent == "allow" {
		return nil, err
	}
	return &turnloop.PlanInvestigationHostRequest{Directory: route.cwd, Decide: func(decision string) error {
		c, err := s.investigationCheckout(ctx, sourceID, owner)
		if err != nil {
			return err
		}
		if c.ID != route.checkout.ID {
			return errors.New("the investigation checkout changed")
		}
		return s.setInvestigationConsent(c.ID, decision)
	}}, nil
}

func (s *Session) executePlanInvestigationCommand(ctx context.Context, sourceID, sourceRoot string, owner worktree.Owner, generation uint64, tool string, params map[string]any) (tools.ToolResult, error) {
	route, err := s.routePlanCommand(ctx, sourceID, sourceRoot, owner, tool, params)
	if err != nil {
		return investigationDenied(err)
	}
	opts := route.inspect
	if opts == nil {
		if route.consent != "allow" {
			return investigationDenied(errors.New("a person must allow commands for this investigation"))
		}
		opts = &fileio.CommandOptions{Prepare: func(cwd string, argv []string, shell command.Shell) (func(command.Result) error, error) {
			if !s.planInvestigationToolMu.TryLock() {
				return nil, errors.New("an investigation operation is still running. Wait for it to finish before starting another command")
			}
			handedOff := false
			defer func() {
				if !handedOff {
					s.planInvestigationToolMu.Unlock()
				}
			}()
			complete, err := s.preparePlanInvestigationLaunch(ctx, sourceID, owner, generation, route, cwd, argv, shell, tool, params)
			if err != nil {
				return nil, err
			}
			handedOff = true
			return func(result command.Result) error {
				defer s.planInvestigationToolMu.Unlock()
				return complete(result)
			}, nil
		}}
	}
	opts.StartContext = ctx
	runParams := maps.Clone(params)
	runParams[route.cwdKey] = route.cwd
	if tool == "exec_command" {
		// Do not run login profiles as part of the experiment.
		runParams["login"] = false
	}
	switch tool {
	case "execute_command":
		return s.executeCommandAutoPromote(ctx, runParams, opts)
	case "exec_command":
		return executeCodexCommand(ctx, s.asyncMgr, route.cwd, tool, runParams, s.commandPolicy(tool, runParams), opts)
	case "async_execute_command":
		handle := tools.StrArg(params, "handle", "")
		if handle == "" {
			return investigationDenied(errors.New("supply a handle for the background command"))
		}
		info, err := s.asyncMgr.Start(handle, route.line, route.cwd, tools.IntArgAny(params, "timeout_seconds", 0), opts)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return tools.ToolResult{Content: FormatCommandStart(*info)}, nil
	}
	return investigationDenied(errors.New("unknown investigation command tool"))
}

func (s *Session) preparePlanInvestigationLaunch(ctx context.Context, sourceID string, owner worktree.Owner, generation uint64, route planCommandRoute, cwd string, argv []string, shell command.Shell, tool string, params map[string]any) (func(command.Result) error, error) {
	// Settings can change while confirmation is open. Verify the shell
	// selected by the launch owner, not only the syntax originally classified.
	policy := s.commandPolicy(tool, params)
	policy.Syntax = shell.Syntax
	if check := safety.CheckCommandSafetyWithPolicy(route.line, s.logger, policy); check.Risk.IsAlwaysBlocked() {
		return nil, fmt.Errorf("command blocked: %s", check.Reason)
	}
	c, err := s.investigationCheckout(ctx, sourceID, owner)
	if err != nil {
		return nil, err
	}
	if c.ID != route.checkout.ID || !sameToolPath(cwd, route.cwd) {
		return nil, errors.New("the investigation checkout or command directory changed")
	}
	if _, err := scopedInvestigationPath(c.Root, cwd, true); err != nil {
		return nil, err
	}
	decision, err := s.investigationConsent(c.ID)
	if err != nil {
		return nil, err
	}
	if decision != "allow" {
		return nil, errors.New("a person must allow commands for this investigation")
	}
	return s.beginInvestigationEvidence(ctx, generation, c, cwd, argv, route.line)
}

// A general shell risk label does not prove an inspection has no executable
// project behavior. Only literal supported invocations run against the source,
// with a trusted executable and direct argv (no shell or project startup).
func planInspection(ctx context.Context, line, root, cwd string) *fileio.CommandOptions {
	if strings.ContainsAny(line, "\r\n;|&<>$()`%!\\") {
		return nil
	}
	args, err := shellwords.Parse(line)
	if err != nil || len(args) == 0 || !planInspectionArgs(args) {
		return nil
	}
	program, err := exec.LookPath(args[0])
	if err != nil || !filepath.IsAbs(program) {
		return nil
	}
	real, err := filepath.EvalSymlinks(program)
	if err != nil || pathInside(root, real) || (runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(real), ".exe")) {
		return nil
	}
	env := make([]string, 0, 8)
	for _, key := range []string{"SystemRoot", "WINDIR", "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "LANG", "LC_ALL", "TERM"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	switch args[0] {
	case "rg":
		args = append([]string{args[0], "--no-config"}, args[1:]...)
	case "git":
		env = append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
		gitFlags := append(gitplatform.Args(), "--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath="+os.DevNull, "-c", "log.showSignature=false")
		if args[1] == "status" {
			// Git status can invoke configured clean/process filters. Read
			// config with an exact non-executing builtin, and disable each
			// reported driver without changing any config or source files.
			config, err := fileio.RunOwnedCommand(ctx, fileio.OwnedCommand{Executable: real, Directory: cwd, Environment: env, OutputLimit: 64 << 10,
				Arguments: append(slices.Clone(gitFlags), "config", "--null", "--name-only", "--get-regexp", `^filter\.`)})
			var exit *exec.ExitError
			if config.Truncated || (err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1 || len(config.Output) != 0)) {
				return nil
			}
			for key := range strings.SplitSeq(string(config.Output), "\x00") {
				if i := strings.LastIndexByte(key, '.'); i > len("filter.") {
					driver := key[:i+1]
					gitFlags = append(gitFlags, "-c", driver+"process=", "-c", driver+"clean=", "-c", driver+"required=false")
				}
			}
			args = append(slices.Clone(args), "--ignore-submodules=all")
		}
		args = append(append([]string{args[0]}, gitFlags...), args[1:]...)
	}
	args[0] = real
	return &fileio.CommandOptions{DirectArgv: args, Environment: env}
}

func planInspectionArgs(args []string) bool {
	switch args[0] {
	case "pwd":
		return len(args) == 1
	case "ls":
		return inspectFlags(args[1:], "-a", "-A", "-l", "-al", "-la", "-h", "-lh", "-lah", "--all")
	case "cat":
		return len(args) > 1 && inspectFlags(args[1:], "-n", "-b")
	case "wc":
		return len(args) > 1 && inspectFlags(args[1:], "-l", "-c", "-w", "-m")
	case "head", "tail":
		return len(args) > 1 && inspectFlags(args[1:], "-n", "-c")
	case "grep":
		return len(args) > 2 && inspectFlags(args[1:], "-n", "-r", "-R", "-i", "-F", "-E", "-l", "-w", "-rn", "-nR")
	case "rg":
		return len(args) > 1 && inspectFlags(args[1:], "-n", "-i", "-l", "-F", "-S", "-w", "-g", "--glob", "--hidden", "--files", "--line-number")
	case "git":
		if len(args) < 2 {
			return false
		}
		switch args[1] {
		case "status":
			return len(args) == 2 || slices.Equal(args[2:], []string{"--short"}) ||
				slices.Equal(args[2:], []string{"--porcelain"}) || slices.Equal(args[2:], []string{"-sb"})
		case "ls-files":
			return len(args) == 2 || slices.Equal(args[2:], []string{"--stage"})
		case "rev-parse":
			return len(args) == 3 && (args[2] == "HEAD" || args[2] == "--show-toplevel" || args[2] == "--show-prefix")
		case "log":
			return len(args) == 3 && args[2] == "--oneline"
		}
	}
	return false
}

func inspectFlags(args []string, allowed ...string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && !slices.Contains(allowed, arg) {
			return false
		}
	}
	return true
}
