package viewmodel

import (
	"testing"

	"crude/localhistory"
)

func TestOverlappingLatestScopesDoesNotGuess(t *testing.T) {
	scopes := []localhistory.MessageScope{{MessageID: "latest", OpenedAt: 20, ClosedAt: 40}, {MessageID: "overlap", OpenedAt: 10, ClosedAt: 25}, {MessageID: "old", OpenedAt: 1, ClosedAt: 5}}
	got := overlappingLatestScopes(scopes)
	if len(got) != 2 || got[0].MessageID != "latest" || got[1].MessageID != "overlap" {
		t.Fatalf("candidates=%+v", got)
	}
}
