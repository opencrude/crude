package app

import "testing"

func TestCrashReportProcessRoles(t *testing.T) {
	for _, tc := range []struct {
		flags CLIFlags
		role  string
	}{
		{CLIFlags{}, "tui"}, {CLIFlags{Core: true}, "core"},
		{CLIFlags{CoreHandover: "/manifest"}, "tui"}, {CLIFlags{SSHHost: "host"}, "tui"},
		{CLIFlags{MCP: true}, ""}, {CLIFlags{CoreStatus: true}, ""}, {CLIFlags{CoreStop: true}, ""},
		{CLIFlags{CoreStdio: true}, ""}, {CLIFlags{CoreHandoverIdentity: true}, ""},
		{CLIFlags{ListSessions: true}, ""}, {CLIFlags{DeleteSession: "id"}, ""},
	} {
		if got := crashReportRole(tc.flags); got != tc.role {
			t.Fatalf("%+v: role %q want %q", tc.flags, got, tc.role)
		}
	}
}
