package safety

import (
	"regexp"
	"strings"

	"crude/types/command"
)

// powerShellLiteralAssignment is a statement that sets a variable to a
// literal: a single-quoted string, a double-quoted string without $ or a
// backtick (so nothing expands), or a number. Bash parsing reads `$env:X='1'`
// as a command name built by expansion and rates the whole line destructive.
// Any other right side, such as `$x = Remove-Item src`, runs a command and
// stays in the checked text. PowerShell also closes strings with typographic
// quotes, so a literal must not contain one: `$x = 'a’; Remove-Item src; '`
// is a string, a command and another string.
var powerShellLiteralAssignment = regexp.MustCompile(`(^|[;\n])[ \t]*\$(?:env:)?[A-Za-z_][A-Za-z0-9_]*[ \t]*=[ \t]*(?:'[^'‘’‚‛\n]*'|"[^"“”„$` + "`" + `\n]*"|[0-9]+(?:\.[0-9]+)?)[ \t]*(?:;|\n|$)`)

// withoutPowerShellLiteralAssignments removes literal assignments, which run
// no code, so that review rates the commands around them.
func withoutPowerShellLiteralAssignments(cmd string) string {
	for {
		next := powerShellLiteralAssignment.ReplaceAllString(cmd, "$1")
		if next == cmd {
			return cmd
		}
		cmd = next
	}
}

// powerShellRunsHiddenCode reports PowerShell constructs that run code which
// the bash parser does not see as a command: ( ) and @( ) subexpressions,
// { } script blocks, the & call operator and <# #> block comments that hide
// what follows from bash. Examples that bash parsing rates as safe but that
// PowerShell runs: `git status @(Remove-Item src)`, `type a; & {Remove-Item
// src}` and `git status <# x #> ; Remove-Item src`.
//
// The test is deliberately textual and ignores quoting. PowerShell accepts
// typographic quotes and backtick escapes, so a quote-aware scanner that
// disagreed with PowerShell by one character would hide code again. A quoted
// parenthesis therefore also needs approval.
func powerShellRunsHiddenCode(cmd string) bool {
	if strings.ContainsAny(cmd, "(){}") || strings.Contains(cmd, "<#") {
		return true
	}
	// "&&" is the PowerShell 7 chain operator and ">&" merges streams (2>&1);
	// bash parsing sees both.
	return strings.Contains(strings.NewReplacer("&&", "", ">&", "").Replace(cmd), "&")
}

// CallSyntax is the syntax that a command tool call runs in: the agent
// command shell, or the shell that exec_command names in its shell argument.
func CallSyntax(tool, shell string, agent command.Syntax) command.Syntax {
	if tool == "exec_command" && strings.TrimSpace(shell) != "" {
		return command.SyntaxFor(strings.TrimSpace(shell))
	}
	return agent
}
