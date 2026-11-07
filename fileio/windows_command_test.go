package fileio

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsBatchScriptKeepsCommandLinePercentMeaning(t *testing.T) {
	env := []string{"Path=C:\\Windows", "CRUDE_TEMP_DIR=C:\\tmp"}
	for _, tc := range []struct{ command, want string }{
		{`curl "https://x.test/a%20b?q=100%"`, `curl "https://x.test/a%%20b?q=100%%"`},
		{`python -c "import time; print(time.strftime('%H:%M:%S'))"`, `python -c "import time; print(time.strftime('%%H:%%M:%%S'))"`},
		{`for %i in (*.go) do echo %i`, `for %%i in (*.go) do echo %%i`},
		{`dir %CRUDE_TEMP_DIR% & echo %path:~0,3% %UNSET%`, `dir %CRUDE_TEMP_DIR% & echo %path:~0,3% %%UNSET%%`},
		{"set NAME=one\r\necho %NAME% %CD%", "set NAME=one\r\necho %NAME% %CD%"},
	} {
		got, err := windowsBatchScript(tc.command, env)
		if err != nil || got != "@echo off\r\n"+tc.want+"\r\n" {
			t.Errorf("%q => %q, %v; want %q", tc.command, got, err, tc.want)
		}
	}
}

// A quoted multi-line script would run its later lines as separate
// commands; it is refused instead. Caret continuation stays allowed.
func TestWindowsBatchScriptRefusesQuotedLineBreaks(t *testing.T) {
	if _, err := windowsBatchScript("python -c \"print(1)\nprint(2)\"", nil); !errors.Is(err, ErrWindowsQuotedLineBreak) {
		t.Fatalf("quoted line break: %v", err)
	}
	if _, err := windowsBatchScript("echo one ^\r\ntwo\r\necho \"three\"", nil); err != nil {
		t.Fatalf("caret continuation and separate lines: %v", err)
	}
}

// The real cmd.exe must print exactly what the command line means.
func TestWindowsCommandsKeepPercentSignsEndToEnd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe semantics")
	}
	out, err := NewLocalExec().Execute(context.Background(), `for %i in (a%20b 50%) do echo [%i]`, ExecOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if strings.TrimSpace(out) != "[a%20b]\n[50%]" {
		t.Fatalf("cmd.exe output %q", out)
	}
}
