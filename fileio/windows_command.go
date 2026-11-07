package fileio

import (
	"errors"
	"regexp"
	"strings"
)

// Windows agent commands run from a temporary .cmd file, because cmd /C
// strips quotes unpredictably. A batch file parses percent signs differently
// from the command line the agent wrote, though: `%20` in a URL, `%s` or `%T`
// in a format string and `%i` in a for loop are all consumed, and a line
// break ends the command even inside a quoted argument, so the rest of a
// multi-line `python -c "..."` would run as separate commands.
//
// windowsBatchScript translates one command line into a batch file with the
// command-line meaning (cmd.exe's documented parse rules):
//   - `%NAME%` expands when NAME is defined (in the environment, a cmd
//     dynamic variable, or assigned by a `set` in the same script),
//     including `%NAME:~a,b%` and `%NAME:x=y%`;
//   - every other `%` is literal, so it is doubled for the batch parser;
//     this also turns the command-line loop variable `%i` into `%%i`.
//
// A quoted argument that spans a line break cannot be passed by cmd.exe at
// all; the command is refused before anything runs.
func windowsBatchScript(command string, environment []string) (string, error) {
	if quotedLineBreak(command) {
		return "", ErrWindowsQuotedLineBreak
	}
	defined := make(map[string]bool, len(environment)+len(cmdDynamicVariables))
	for _, entry := range environment {
		if name, _, ok := strings.Cut(entry, "="); ok && name != "" {
			defined[strings.ToUpper(name)] = true
		}
	}
	for _, name := range cmdDynamicVariables {
		defined[name] = true
	}
	for _, match := range batchAssignment.FindAllStringSubmatch(command, -1) {
		defined[strings.ToUpper(match[1])] = true
	}
	var out strings.Builder
	out.Grow(len(command) + 16)
	for i := 0; i < len(command); i++ {
		if command[i] != '%' {
			out.WriteByte(command[i])
			continue
		}
		if end := strings.IndexAny(command[i+1:], "%\r\n"); end > 0 && command[i+1+end] == '%' {
			reference := command[i+1 : i+1+end]
			name, _, _ := strings.Cut(reference, ":")
			if name != "" && defined[strings.ToUpper(name)] {
				out.WriteString(command[i : i+end+2])
				i += end + 1
				continue
			}
		}
		out.WriteString("%%")
	}
	return "@echo off\r\n" + out.String() + "\r\n", nil
}

// ErrWindowsQuotedLineBreak is a tool-level result the model can act on.
var ErrWindowsQuotedLineBreak = errors.New("cmd.exe cannot pass a line break inside a quoted argument, so this command was not run. Write the script to a file in the temporary directory (CRUDE_TEMP_DIR) and run that file, or put the command on one line")

// cmdDynamicVariables are defined by cmd.exe itself, not the environment.
var cmdDynamicVariables = []string{"CD", "DATE", "TIME", "RANDOM", "ERRORLEVEL", "CMDEXTVERSION", "CMDCMDLINE", "HIGHESTNUMANODENUMBER", "__APPDIR__", "__CD__"}

// batchAssignment finds `set NAME=`, `set "NAME=` and `set /a|/p NAME=`.
var batchAssignment = regexp.MustCompile(`(?i)(?:^|[\s&|(])set\s+(?:/[ap]\s+)?"?([^\s=%"&|<>^]+)\s*=`)

// quotedLineBreak follows cmd.exe's quote state, which a line break resets.
// A `^` before a line break outside quotes continues the line.
func quotedLineBreak(command string) bool {
	quoted := false
	for i := 0; i < len(command); i++ {
		switch command[i] {
		case '"':
			quoted = !quoted
		case '^':
			if !quoted {
				i++ // escapes the next character, including a line break
			}
		case '\r', '\n':
			if quoted {
				return true
			}
		}
	}
	return false
}
