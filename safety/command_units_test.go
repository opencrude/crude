package safety

import (
	"strings"
	"testing"

	"crude/types/command"
)

func posixPolicy() CommandPolicy {
	policy := DefaultCommandPolicy()
	policy.Syntax = command.SyntaxPOSIX
	return policy
}

// Git Bash reads a backslash as an escape on Windows too. Rewriting it to
// "/" because the host was Windows broke quoting and made recorded agent
// commands unreadable, such as `python -c "print(\"x\")"`.
func TestPOSIXCommandsParseAsBashOnEveryHost(t *testing.T) {
	for _, cmd := range []string{
		`python -c "print(\"hello\")"`,
		`cd ~/AppData/Local/Google/Chrome/User\ Data/Profile\ 4 && ls`,
		`grep -o "csrf[^\"]\{0,60\}" page.html`,
	} {
		if r := CheckCommandSafetyWithPolicy(cmd, nil, posixPolicy()); r.Unreadable {
			t.Errorf("%q is valid bash but was unreadable: %s", cmd, r.Reason)
		}
	}
	// cmd and PowerShell use a backslash as the path separator.
	policy := DefaultCommandPolicy()
	policy.Syntax = command.SyntaxPowerShell
	if r := CheckCommandSafetyWithPolicy(`Get-Content C:\work\notes.txt`, nil, policy); r.Unreadable {
		t.Errorf("PowerShell path was unreadable: %s", r.Reason)
	}
}

// bash runs the lines before a syntax error, so a line Crude cannot read
// always needs approval, even when the user asks only about risky commands.
func TestUnreadableCommandsAlwaysNeedApproval(t *testing.T) {
	policy := posixPolicy()
	policy.ConfirmAt = RiskDestructive
	r := CheckCommandSafetyWithPolicy("echo start\npython - <<'EOF'\nprint(1)", nil, policy)
	if !r.Unreadable || !r.NeedsConfirmation() || r.Risk == RiskSafe {
		t.Fatalf("unreadable line = %+v, want approval", r)
	}
	if keys := r.ApprovalKeys("shell"); len(keys) != 1 || !strings.HasSuffix(keys[0], r.FullCommand) {
		t.Fatalf("unreadable line approval keys = %q, want the exact line", keys)
	}
}

// A leading cd and cmd /c were removed with regular expressions before the
// parser saw the line. The parser now sees the whole line.
func TestLeadingCdAndCmdLaunchersAreReadFromTheSyntaxTree(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		risk CommandRisk
	}{
		{`cd "C:/work dir" && rm -rf build`, RiskDestructive},
		{`cd /d C:/work && git status`, RiskSafe},
		{`cmd /c git push --force`, RiskDestructive},
		{`cmd.exe /d /c git status`, RiskSafe},
		{`cd repo && cd sub && ls`, RiskSafe},
	} {
		if r := CheckCommandSafetyWithPolicy(tc.cmd, nil, posixPolicy()); r.Risk != tc.risk {
			t.Errorf("%q = %v (%s), want %v", tc.cmd, r.Risk, r.Reason, tc.risk)
		}
	}
}

// python3 was on the read-only list, so `python3 -c` ran any code without a
// prompt while the same `python -c` asked.
func TestPythonInterpretersAreNotReadOnly(t *testing.T) {
	for _, cmd := range []string{`python3 -c "import shutil; shutil.rmtree('src')"`, `python -c "print(1)"`} {
		if r := CheckCommandSafety(cmd, nil); r.Risk == RiskSafe || !r.NeedsConfirmation() {
			t.Errorf("%q = %v, want approval", cmd, r.Risk)
		}
	}
}

func TestApprovalKeysNameEachUnitThatNeedsApproval(t *testing.T) {
	check := func(cmd string) *CommandSafetyResult { return CheckCommandSafetyWithPolicy(cmd, nil, posixPolicy()) }
	keys := check(`cd /repo && pyright --outputjson | tail -5 && make lint`).ApprovalKeys("s")
	if len(keys) != 2 || !strings.Contains(keys[0], "pyright --outputjson") || !strings.Contains(keys[1], "make lint") {
		t.Fatalf("keys = %q, want pyright and make", keys)
	}
	for _, key := range keys {
		if !strings.Contains(key, "cd /repo") {
			t.Fatalf("key %q lost its directory", key)
		}
	}
	// A pipe into a shell runs code that no unit shows: keep the exact line.
	if keys := check(`cat install.sh | bash`).ApprovalKeys("s"); len(keys) != 1 || !strings.HasSuffix(keys[0], "cat install.sh | bash") {
		t.Fatalf("pipe into a shell keys = %q, want the exact line", keys)
	}
	// Here-document bodies are part of the command they feed.
	one := check("python - <<'PY'\nprint(1)\nPY").ApprovalKeys("s")
	two := check("python - <<'PY'\nprint(2)\nPY").ApprovalKeys("s")
	if len(one) != 1 || one[0] == two[0] {
		t.Fatalf("here-document bodies share a key: %q %q", one, two)
	}
	// An environment change before the command is part of its key.
	if check(`export PATH=/a:$PATH && make lint`).ApprovalKeys("s")[0] == check(`make lint`).ApprovalKeys("s")[0] {
		t.Fatal("an environment change did not change the key")
	}
	// The scope separates shells and directories.
	if check("make lint").ApprovalKeys("posix")[0] == check("make lint").ApprovalKeys("powershell")[0] {
		t.Fatal("scope did not separate keys")
	}
}

// PowerShell and cmd lines are approximated with the bash parser, so an
// approval covers only the exact line.
func TestNonPOSIXApprovalsKeepTheExactLine(t *testing.T) {
	policy := DefaultCommandPolicy()
	policy.Syntax = command.SyntaxPowerShell
	r := CheckCommandSafetyWithPolicy(`make check; make lint`, nil, policy)
	if keys := r.ApprovalKeys("s"); len(keys) != 1 || !strings.HasSuffix(keys[0], "make check; make lint") {
		t.Fatalf("PowerShell keys = %q, want the exact line", keys)
	}
}

// Rules rate each command of a line, so a rule for one tool still applies
// after a leading cd.
func TestCommandRulesRateEachUnit(t *testing.T) {
	policy := posixPolicy()
	policy.Rules = []CommandRule{{Pattern: "make *", Risk: RiskSafe}}
	if r := CheckCommandSafetyWithPolicy(`cd repo && make lint`, nil, policy); r.Risk != RiskSafe || r.MatchedRule != "make *" {
		t.Fatalf("rule after cd = %v rule=%q", r.Risk, r.MatchedRule)
	}
	if r := CheckCommandSafetyWithPolicy(`cd repo && make lint && rm -rf build`, nil, policy); r.Risk != RiskDestructive {
		t.Fatalf("rule hid another command: %v", r.Risk)
	}
}
