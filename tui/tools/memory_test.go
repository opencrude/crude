package tools

import (
	"strings"
	"testing"
)

func memoryEvidenceText(result StructuredResult) string {
	var text strings.Builder
	text.WriteString(result.Summary)
	for _, section := range result.Sections {
		text.WriteString(section.Title + section.Metadata + section.Body)
		for _, field := range section.Details {
			text.WriteString("\n" + field.Label + ": " + field.Value)
		}
	}
	return text.String()
}

func requireMemoryContains(t *testing.T, result StructuredResult, parts ...string) {
	t.Helper()
	text := memoryEvidenceText(result)
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("missing %q in projected result %q", part, text)
		}
	}
}

func TestMemoryMutationsKeepAuthoredEvidence(t *testing.T) {
	long := strings.Repeat("full authored assertion across the terminal ", 12)
	evidence := `[{"kind":"validation","outcome":"succeeded","session_id":"s1","tool_call_id":"call-47","summary":"integration passed"}]`
	cases := []struct {
		name, result, input string
		want                []string
	}{
		{"remember", `{"id":"m1","revision":1,"kind":"lesson","title":"Keep changes","status":"remembered"}`, `{"kind":"lesson","title":"Keep changes","content":` + jsonString(long) + `,"importance":90,"pinned":true,"paths":["src/a.go"],"applies_when":"on retry","evidence":` + evidence + `}`, []string{"m1", "Revision: 1", "lesson", "Keep changes", long, "Importance: 90", "Pinned: true", "src/a.go", "on retry", "validation", "succeeded", "s1", "call-47", "integration passed"}},
		{"update_memory", `{"id":"m1","revision":2,"previous_revision":1,"status":"updated"}`, `{"id":"m1","expected_revision":1,"title":"Updated assertion","content":` + jsonString(long) + `,"evidence":` + evidence + `}`, []string{"m1", "Revision: 2", "Previous revision: 1", "Expected revision: 1", long, "call-47"}},
		{"forget_memory", `{"id":"m1","revision":3,"status":"forgotten"}`, `{"id":"m1","expected_revision":2}`, []string{"m1", "Revision: 3", "Expected revision: 2", "forgotten"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projection := BuildStructuredResult(tc.name, tc.result, tc.input)
			if len(projection.Sections) != 1 || projection.Summary == "" {
				t.Fatalf("missing semantic mutation projection: %+v", projection)
			}
			requireMemoryContains(t, projection, tc.want...)
		})
	}
}

func jsonString(value string) string {
	// This fixture has no JSON-sensitive characters.
	return `"` + value + `"`
}

func TestMemoryRecallFullEvidenceAndEmptyFallback(t *testing.T) {
	long := strings.Repeat("entire remembered content ", 15)
	content := `{"total":1,"results":[{"id":"m5","revision":8,"title":"Build process","content":` + jsonString(long) + `,"kind":"convention","status":"archived","last_updated":"3d ago","source":"user","from_session":"s4","pinned":false,"importance":64,"applies_when":"CI fails","evidence_count":1,"evidence":[{"session_id":"s4","message_id":"msg-3","kind":"user_statement","summary":"The user requested it","outcome":"unmeasured"}]}]}`
	projection := BuildStructuredResult("recall_memory", content, `{}`)
	if len(projection.Sections) != 1 || projection.Sections[0].Body != long {
		t.Fatalf("recall lost complete content: %+v", projection)
	}
	requireMemoryContains(t, projection, "m5", "Revision: 8", "archived", "3d ago", "Source: user", "From session: s4", "Pinned: false", "CI fails", "Evidence count: 1", "msg-3", "user_statement", "unmeasured")
	for _, plain := range []string{`No workspace memories matched "x".`, "not valid { json"} {
		fallback := BuildStructuredResult("recall_memory", plain, `{}`)
		if fallback.Summary == "Recalled" || !strings.Contains(memoryEvidenceText(fallback), plain) {
			t.Fatalf("recall fallback lost plain result %q: %+v", plain, fallback)
		}
	}
}

func TestMemoryFeedbackCarriesExactRevisionAndAttribution(t *testing.T) {
	projection := BuildStructuredResult("memory_feedback", `{"status":"recorded","count":2}`, `{"items":[{"id":"m1","revision":3,"kind":"useful","evidence":[{"session_id":"s9","message_id":"msg-2","kind":"user_statement","outcome":"unmeasured","summary":"User approved"}]},{"id":"m2","revision":11,"kind":"contradicted","evidence":[{"session_id":"s10","tool_call_id":"tool-55","kind":"validation","outcome":"failed","summary":"Build failed"}]}]}`)
	if len(projection.Sections) != 2 {
		t.Fatalf("missing feedback items: %+v", projection)
	}
	requireMemoryContains(t, projection, "m1", "Revision: 3", "useful", "s9", "msg-2", "User approved", "m2", "Revision: 11", "contradicted", "s10", "tool-55", "failed", "Build failed")
}

func TestMemoryHistoryKeepsAuthorAndFiles(t *testing.T) {
	user := strings.Repeat("user asked for detailed changes ", 15)
	assistant := strings.Repeat("assistant delivered complete changes ", 17)
	projection := BuildStructuredResult("search_history", `{"sessions":[{"session_id":"s5","title":"Release","summary":"Shipped the release","outcome":"passed","key_files":["a.go","b.go"],"when":"4d ago"}],"turns":[{"session_id":"s2","turn":13,"when":"2h ago","user":`+jsonString(user)+`,"assistant":`+jsonString(assistant)+`,"tools":"grep_files read_file","files":"src/c.go src/d.go"}]}`, `{"query":"release"}`)
	if len(projection.Sections) != 2 || projection.Sections[1].Body != "User: "+user+"\nAssistant: "+assistant {
		t.Fatalf("history result lost turn authorship: %+v", projection)
	}
	requireMemoryContains(t, projection, "Session ID: s5", "Release", "Shipped the release", "passed", "a.go, b.go", "Session ID: s2", "Turn: 13", "User: "+user, "Assistant: "+assistant, "Tools: grep_files read_file", "Files: src/c.go src/d.go")
	for _, plain := range []string{`No past turns or session summaries matched "nothing".`} {
		fallback := BuildStructuredResult("search_history", plain, `{}`)
		if fallback.Summary == "Searched history" || !strings.Contains(memoryEvidenceText(fallback), plain) {
			t.Fatalf("history fallback lost plain result %q: %+v", plain, fallback)
		}
	}
	if fallback := BuildStructuredResult("search_history", `{"bad":`, `{}`); strings.Contains(memoryEvidenceText(fallback), `{"bad"`) {
		t.Fatalf("invalid structured result leaked raw JSON: %+v", fallback)
	}
}
