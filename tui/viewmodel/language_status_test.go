package viewmodel

import (
	"strings"
	"testing"

	"crude/types"
)

func TestLanguageStatusAcrossSurfaces(t *testing.T) {
	for _, tc := range []struct {
		state                  types.LanguageServiceState
		label, message, footer string
		action                 types.LanguageServiceAction
	}{
		{types.LanguageRestricted, "Restricted mode", "Language servers do not run in restricted mode.", "Problems: off", types.LanguageActionTrustFolder},
		{types.LanguageOff, "Language servers off", "Language servers are off.", "Problems: off", types.LanguageActionOpenSettings},
		{types.LanguageNoServer, "", "No language server for Go.", "Problems", types.LanguageActionOpenSettings},
		{types.LanguageIdle, "", "No results yet.", "Problems", types.LanguageActionNone},
		{types.LanguageStarting, "Checking…", "Starting gopls.", "Problems", types.LanguageActionNone},
		{types.LanguageReady, "", "No results yet.", "Problems", types.LanguageActionNone},
		{types.LanguageFailed, "Language server failed", "Language server failed. Check settings. Restart the server.", "Problems: error", types.LanguageActionRestartServer},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			app, _, service, _ := qualifiedDiagnosticApp(t)
			service.insights.Language = types.LanguageServiceStatus{State: tc.state, Label: tc.label, Message: tc.message, Action: tc.action, ActionLabel: "Next"}
			_, header, _ := app.activeHeaderContext()
			if header != tc.label {
				t.Fatalf("header = %q, want %q", header, tc.label)
			}
			footer := app.footerStatusSegments()[0].Text
			if footer != tc.footer {
				t.Fatalf("footer = %q, want %q", footer, tc.footer)
			}
			items := app.problemPanelItems()
			if items[0].Summary != tc.message {
				t.Fatalf("health = %q", items[0].Summary)
			}
			text := header + " " + footer + " " + items[0].Summary
			for _, word := range []string{"pending", "unverified", "partial", "context unknown", "grant", "admission", "publication", "generation", "hidden", "Static only", "Project execution", "Syntax shared", "0 errors", "0 warnings"} {
				if strings.Contains(text, word) {
					t.Fatalf("internal word %q in %q", word, text)
				}
			}
			if tc.action == types.LanguageActionTrustFolder && items[0].PrimaryAction != "language:trust" {
				t.Fatal("trust action not routed")
			}
			if tc.action == types.LanguageActionStartServers && items[0].PrimaryAction != "lsp:start-all" {
				t.Fatal("start action not routed")
			}
		})
	}
}
