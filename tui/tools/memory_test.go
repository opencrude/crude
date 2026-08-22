package tools

import "testing"

func TestMemoryResultSummary(t *testing.T) {
	cases := []struct {
		tool, content string
		want          string
	}{
		{"remember", `{"id":"m1","kind":"fact","title":"Use tabs","status":"remembered"}`, "Remembered — Use tabs"},
		{"update_memory", `{"id":"m1","replaced":"m0","status":"updated"}`, "Updated memory — m1"},
		{"forget_memory", `{"status":"forgotten","id":"m1"}`, "Forgot memory — m1"},
		{"recall_memory", `{"results":[{"id":"m1"}],"total":1}`, "Recalled — 1 memories"},
		{"search_history", `{"turns":[{"session_id":"s1"}],"sessions":[]}`, "Searched history — 1 results"},
	}
	for _, c := range cases {
		got := MemoryResultSummary(c.tool, c.content)
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.tool, got, c.want)
		}
	}
}
