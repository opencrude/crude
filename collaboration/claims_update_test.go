package collaboration

import (
	"errors"
	"strings"
	"testing"
)

func TestUpdateClaimsRejectsActiveOrForeignParent(t *testing.T) {
	store, _ := openTestStore(t)
	parent, child := registerTestAgents(t, store)
	run, err := store.StartRun(t.Context(), StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, Origin: OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, TerminalRunID: run.ID, ExpectedRunVersion: run.Version, Paths: []string{"a"}}); err == nil {
		t.Fatal("active child claim repair succeeded")
	}
	finished, err := store.FinishRun(t.Context(), FinishRunRequest{RunID: run.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: run.Version, State: RunInterrupted, Outcome: "blocked"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: "foreign", AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, TerminalRunID: run.ID, ExpectedRunVersion: finished.Version, Paths: []string{"a"}}); err == nil {
		t.Fatal("foreign parent claim repair succeeded")
	}
}

func TestUpdateClaimsNarrowsOriginalCeilingAtomicallyAndAudits(t *testing.T) {
	store, _ := openTestStore(t)
	parent, child := registerTestAgents(t, store)
	run, err := store.StartRun(t.Context(), StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: "assignment-1", Origin: OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{AgentID: child.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{"src"}}); err != nil {
		t.Fatal(err)
	}
	finished, err := store.FinishRun(t.Context(), FinishRunRequest{RunID: run.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: run.Version, State: RunInterrupted, Outcome: "blocked"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, TerminalRunID: run.ID, ExpectedRunVersion: finished.Version, ExpectedVersions: map[string]uint64{}, Paths: []string{"src/one.go", "src/[literal].go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Claims) != 2 || result.Audit.ID == "" || result.Audit.Kind != EventPeerNote {
		t.Fatalf("result = %+v", result)
	}
	// The audit body is delivered into the child's transcript; it is prose,
	// never the raw claim JSON.
	if strings.HasPrefix(result.Audit.Body, "{") || !strings.Contains(result.Audit.Body, "one.go") {
		t.Fatalf("audit body is not readable: %q", result.Audit.Body)
	}
	versions := map[string]uint64{}
	for _, claim := range result.Claims {
		versions[claim.ID] = claim.Version
	}
	if _, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, TerminalRunID: run.ID, ExpectedRunVersion: finished.Version, ExpectedVersions: versions, Paths: []string{"outside.go"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("widening error = %v", err)
	}
	active, err := store.Claims(t.Context(), child.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Fatalf("failed repair changed active claims: %+v", active)
	}
	if _, err := store.StartRun(t.Context(), StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: "foreign-assignment", Origin: OriginPeer}); !errors.Is(err, ErrConflict) {
		t.Fatalf("different assignment adopted preserved declarations: %v", err)
	}
	actor, err := store.Agent(t.Context(), child.ID)
	if err != nil || actor.CurrentRunID != "" {
		t.Fatalf("failed start published a replacement run: %+v error=%v", actor, err)
	}
	resumed, err := store.StartRun(t.Context(), StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: run.RequestID, Origin: OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{AgentID: child.ID, RunID: resumed.ID, RequestID: run.RequestID, Paths: []string{"src/one.go", "src/[literal].go"}})
	if err != nil || len(bound) != 2 {
		t.Fatalf("explicit continuation could not use its repaired scope: claims=%+v error=%v", bound, err)
	}
	for i, claim := range bound {
		if claim.RunID != resumed.ID || claim.ID != result.Claims[i].ID || claim.Version != result.Claims[i].Version+1 {
			t.Fatalf("continuation recreated or failed to fence a declaration: %+v", claim)
		}
	}
}

func TestUpdateClaimsRequiresExactCompletePreimage(t *testing.T) {
	store, _ := openTestStore(t)
	parent, child := registerTestAgents(t, store)
	run, err := store.StartRun(t.Context(), StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: "assignment-2", Origin: OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{AgentID: child.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{"src"}}); err != nil {
		t.Fatal(err)
	}
	finished, err := store.FinishRun(t.Context(), FinishRunRequest{RunID: run.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: run.Version, State: RunInterrupted})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, TerminalRunID: run.ID, ExpectedRunVersion: finished.Version, ExpectedVersions: map[string]uint64{}, Paths: []string{"src/one.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, TerminalRunID: run.ID, ExpectedRunVersion: finished.Version, ExpectedVersions: map[string]uint64{}, Paths: []string{"src/two.go"}}); !errors.Is(err, ErrStale) {
		t.Fatalf("missing preimage error = %v (first=%+v)", err, first)
	}
}

func TestNarrowActiveClaimsRequiresExactActorRunAndStrictSubset(t *testing.T) {
	store, _ := openTestStore(t)
	parent, _ := registerTestAgents(t, store)
	run, err := store.StartRun(t.Context(), StartRunRequest{AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RequestID: "parent-task", Origin: OriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{AgentID: parent.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{"src"}})
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]uint64{claims[0].ID: claims[0].Version}
	if _, err := store.NarrowActiveClaims(t.Context(), NarrowActiveClaimsRequest{AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: run.Version, ExpectedVersions: versions, Paths: []string{"src"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-scope error = %v", err)
	}
	result, err := store.NarrowActiveClaims(t.Context(), NarrowActiveClaimsRequest{AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: run.Version, ExpectedVersions: versions, Paths: []string{"src/owned.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Released) != 1 || len(result.Claims) != 1 || result.Claims[0].RunID != run.ID || result.Claims[0].Path != normalizedTestPath(t, "src/owned.go") {
		t.Fatalf("narrow result = %+v", result)
	}
	if _, err := store.NarrowActiveClaims(t.Context(), NarrowActiveClaimsRequest{AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: run.Version, ExpectedVersions: versions, Paths: nil}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale preimage error = %v", err)
	}
}

func TestNarrowActiveClaimsAllowsExplicitRelinquishWithoutEndingRun(t *testing.T) {
	store, _ := openTestStore(t)
	parent, _ := registerTestAgents(t, store)
	run, err := store.StartRun(t.Context(), StartRunRequest{AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RequestID: "parent-task", Origin: OriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{AgentID: parent.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{"src"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.NarrowActiveClaims(t.Context(), NarrowActiveClaimsRequest{AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: run.Version, ExpectedVersions: map[string]uint64{claims[0].ID: claims[0].Version}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Released) != 1 || len(result.Claims) != 0 {
		t.Fatalf("relinquish result = %+v", result)
	}
	actor, err := store.Agent(t.Context(), parent.ID)
	if err != nil || actor.CurrentRunID != run.ID {
		t.Fatalf("relinquish changed active run: %+v err=%v", actor, err)
	}
}

func normalizedTestPath(t *testing.T, path string) string {
	t.Helper()
	normalized, err := normalizedPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}
