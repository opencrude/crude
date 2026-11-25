package providersim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestChatCachePrefixTracksActualConversationChanges(t *testing.T) {
	const tool = `[{"function":{"name":"echo"},"type":"function"}]`
	const firstUser = `{"content":"One","role":"user"}`
	const assistant = `{"content":"First answer","role":"assistant"}`
	const nextUser = `{"content":"Two","role":"user"}`
	systemText := strings.Repeat("é", 2200)
	system := `{"role":"system","content":"` + systemText + `"}`
	initial := `[ ` + system + `,` + firstUser + ` ]`
	continued := `[` + strings.Join([]string{system, firstUser, assistant, nextUser}, ",") + `]`
	// Independently count the bytes in the three initial blocks. The non-ASCII
	// system text crosses 1024 only if the simulator counts UTF-8 bytes.
	firstInput := 12 + 1108 + 8
	if got := len(tool); got != 48 {
		t.Fatalf("fixture tool length = %d", got)
	}
	s := New(t, WithCacheProfile(Chat, "another-model", CacheProfile{Minimum: 16, Granularity: 8}))
	s.Script(Text("First answer"), Text("Second answer"), Text("Changed system"), Text("Changed tools"), Text("New model"), Text("Same new model"), Text("Removed tools"), Text("Shorter input"))
	for i, tc := range []struct {
		model, messages, tools                             string
		previous, sharedBlocks, sharedTokens, cachedTokens int
		difference                                         string
	}{
		{"gpt-4o-mini", initial, tool, 0, 0, 0, 0, ""},
		{"gpt-4o-mini", continued, tool, 1, 3, firstInput, 1024, "/messages/2"},
		{"gpt-4o-mini", strings.Replace(continued, "é", "ø", 1), tool, 1, 1, 12, 0, "/messages/0"},
		{"gpt-4o-mini", continued, strings.Replace(tool, "echo", "other", 1), 1, 0, 0, 0, "/tools"},
		{"another-model", initial, tool, 0, 0, 0, 0, ""},
		{"another-model", continued, tool, 5, 3, firstInput, 1128, "/messages/2"},
		{"gpt-4o-mini", continued, "[]", 1, 0, 0, 0, "/tools"},
		{"gpt-4o-mini", `[` + system + `]`, tool, 1, 2, 1120, 1024, "/messages/1"},
	} {
		input := fmt.Sprintf(`{"model":%q,"messages":%s,"tools":%s}`, tc.model, tc.messages, tc.tools)
		body := jsonResponse(t, post(t, s, input), http.StatusOK, "CreateChatCompletionResponse")
		last, _ := s.Last()
		p := last.Prefix
		if p.PreviousIndex != tc.previous || p.SharedBlocks != tc.sharedBlocks || p.SharedTokens != tc.sharedTokens || p.CachedTokens != tc.cachedTokens || p.FirstDifference != tc.difference {
			t.Errorf("turn %d prefix = %+v; want earlier=%d blocks=%d shared=%d cached=%d diff=%q", i+1, p, tc.previous, tc.sharedBlocks, tc.sharedTokens, tc.cachedTokens, tc.difference)
		}
		usage := body["usage"].(map[string]any)
		if usage["prompt_tokens"] != json.Number(strconv.Itoa(p.InputTokens)) || usage["prompt_tokens_details"].(map[string]any)["cached_tokens"] != json.Number(strconv.Itoa(tc.cachedTokens)) {
			t.Errorf("turn %d wire usage = %#v, prefix = %+v", i+1, usage, p)
		}
		if i == 0 && p.InputTokens != firstInput {
			t.Errorf("initial prompt = %d tokens, want %d", p.InputTokens, firstInput)
		}
	}
}

func TestChatPrefixPrefersLongestEarlierBranch(t *testing.T) {
	s := New(t)
	s.Script(Text("one"), Text("two"), Text("three"))
	for _, body := range []string{
		`{"model":"gpt-4o-mini","messages":[{"role":"system","content":"system"},{"role":"user","content":"fork one"}]}`,
		`{"model":"gpt-4o-mini","messages":[{"role":"system","content":"system"},{"role":"user","content":"fork two"}]}`,
		`{"model":"gpt-4o-mini","messages":[{"content":"system","role":"system"},{"content":"fork two","role":"user"},{"role":"assistant","content":"two"}]}`,
	} {
		jsonResponse(t, post(t, s, body), http.StatusOK, "CreateChatCompletionResponse")
	}
	last, _ := s.Last()
	if p := last.Prefix; p.PreviousIndex != 2 || p.SharedBlocks != 3 || p.FirstDifference != "/messages/2" {
		t.Fatalf("longest matching prior branch = %+v", p)
	}
}

func TestPrefixCacheWriteOnlyNewEligibleTokens(t *testing.T) {
	profile := CacheProfile{Minimum: 5, Granularity: 4}
	first := []block{{path: "/input/0", text: strings.Repeat("A", 44)}}                         // 11 tokens; 9 eligible.
	next := append(slices.Clone(first), block{path: "/input/1", text: strings.Repeat("B", 20)}) // 16; 13 eligible.
	root := new(prefixNode)
	for i, tc := range []struct {
		blocks      []block
		profile     CacheProfile
		read, write int
	}{
		{first, profile, 0, 9}, {first, profile, 9, 0}, {next, profile, 9, 4},
		{next, CacheProfile{}, 0, 0}, {first, CacheProfile{Minimum: 12, Granularity: 4}, 0, 0},
	} {
		got := root.record(i+1, tc.blocks, tc.profile)
		if got.CachedTokens != tc.read || got.CacheWriteTokens != tc.write {
			t.Errorf("turn %d read=%d write=%d, want read=%d write=%d", i+1, got.CachedTokens, got.CacheWriteTokens, tc.read, tc.write)
		}
	}
}
