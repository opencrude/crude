package viewmodel

import (
	"strings"
	"testing"

	"crude/types/insight"
)

func TestLanguageStatusAcrossSurfaces(t *testing.T) {
	for _, tc := range []struct {
		state                  insight.LanguageServiceState
		label, message, footer string
		action                 insight.LanguageServiceAction
	}{
		{insight.LanguageRestricted, "Restricted mode", "Language servers do not run in restricted mode.", "Problems: off", insight.LanguageActionTrustFolder},
		{insight.LanguageOff, "Language servers off", "Language servers are off.", "Problems: off", insight.LanguageActionOpenSettings},
		{insight.LanguageNoServer, "", "No language server for Go.", "Problems", insight.LanguageActionOpenSettings},
		{insight.LanguageIdle, "", "No results yet.", "Problems", insight.LanguageActionNone},
		{insight.LanguageStarting, "Checking…", "Starting gopls.", "Problems", insight.LanguageActionNone},
		{insight.LanguageReady, "", "No results yet.", "Problems", insight.LanguageActionNone},
		{insight.LanguageFailed, "Language server failed", "Language server failed. Check settings. Restart the server.", "Problems: error", insight.LanguageActionRestartServer},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			app, _, service, _ := qualifiedDiagnosticApp(t)
			service.insights.Language = insight.LanguageServiceStatus{State: tc.state, Label: tc.label, Message: tc.message, Action: tc.action, ActionLabel: "Next"}
			_, header, _ := app.activeHeaderContext()
			if header != tc.label {
				t.Fatalf("header = %q, want %q", header, tc.label)
			}
			footer := app.footerStatusSegments()[0].Text
			if footer != tc.footer {
				t.Fatalf("footer = %q, want %q", footer, tc.footer)
			}
			status := app.problemsModel().Status
			if tc.action != insight.LanguageActionNone && status.Text != tc.message {
				t.Fatalf("language status = %q, want %q", status.Text, tc.message)
			}
			text := header + " " + footer + " " + status.Text
			for _, word := range []string{"pending", "unverified", "partial", "context unknown", "grant", "admission", "publication", "generation", "hidden", "Static only", "Project execution", "Syntax shared", "0 errors", "0 warnings"} {
				if strings.Contains(text, word) {
					t.Fatalf("internal word %q in %q", word, text)
				}
			}
			if tc.action == insight.LanguageActionTrustFolder && status.Action != "language:trust" {
				t.Fatal("trust action not routed")
			}
			if tc.action == insight.LanguageActionStartServers && status.Action != "lsp:start-all" {
				t.Fatal("start action not routed")
			}
		})
	}
}
