package prompt

import (
	"strings"
	"testing"
)

func TestToolSetupMessageSeparatesSetUpFromLeaveAlone(t *testing.T) {
	text := ToolSetupMessage(ToolSetupTask{
		Project: "/work/<app>",
		Setup:   []ToolSetupItem{{Languages: []string{"go"}, Package: "gopls", Command: "gopls", Status: "Not installed."}},
		Skip:    []ToolSetupItem{{Languages: []string{"c"}, Package: "clangd", Command: "clangd"}},
	})
	setUp := between(text, "<set_up>", "</set_up>")
	leave := between(text, "<leave_alone>", "</leave_alone>")
	if !strings.Contains(setUp, `package="gopls"`) || strings.Contains(setUp, "clangd") {
		t.Fatalf("set_up must hold only the switched-on tool:\n%s", text)
	}
	if !strings.Contains(leave, `package="clangd"`) || strings.Contains(leave, "gopls") {
		t.Fatalf("leave_alone must hold only the switched-off tool:\n%s", text)
	}
	if strings.Contains(text, "<app>") || !strings.Contains(text, "&lt;app&gt;") {
		t.Fatalf("project text must be escaped:\n%s", text)
	}
}

func TestToolSetupMessageKeepsTheBoundaries(t *testing.T) {
	text := ToolSetupMessage(ToolSetupTask{Project: "/p"})
	if !strings.Contains(text, "<set_up/>") || !strings.Contains(text, "<leave_alone/>") {
		t.Fatalf("empty lists must still be present:\n%s", text)
	}
	for _, rule := range []string{"Search the web", "official documentation", "not a required installation recipe",
		"administrator rights", "lsp.languages.&lt;id&gt;.command", "Ask once", "Do not retry in a loop"} {
		if !strings.Contains(text, rule) {
			t.Fatalf("missing rule %q:\n%s", rule, text)
		}
	}
}

func between(text, open, closing string) string {
	start := strings.Index(text, open)
	end := strings.Index(text, closing)
	if start < 0 || end < start {
		return ""
	}
	return text[start:end]
}
