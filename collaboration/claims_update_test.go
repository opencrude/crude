package collaboration

import (
	"errors"
	"strings"
	"testing"
)

func TestUpdateClaimsReplacesLiveRunScopeWithoutTouchingTheRun(t *testing.T) {
	store, _ := openTestStore(t)
	parent, child := registerTestAgents(t, store)
	run, err := store.StartRun(t.Context(), StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: "assignment-live", Origin: OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{AgentID: child.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{"src"}})
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]uint64{owned[0].ID: owned[0].Version}
	if _, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: "foreign", AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: run.Version, ExpectedVersions: versions, Paths: []string{"src", "docs"}}); !errors.Is(err, ErrStale) {
		t.Fatalf("foreign parent error = %v", err)
	}
	if _, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: run.Version + 1, ExpectedVersions: versions, Paths: []string{"src", "docs"}}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale run version error = %v", err)
	}
	// A widening beyond the launch scope is the parent's delegation decision.
	result, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: run.Version, ExpectedVersions: versions, Paths: []string{"src", "docs"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Released) != 1 || len(result.Claims) != 2 || !strings.Contains(result.Audit.Body, "allowed from now on") {
		t.Fatalf("live update result = %+v", result)
	}
	for _, claim := range result.Claims {
		if claim.RunID != run.ID {
			t.Fatalf("live scope was not bound to the running attempt: %+v", claim)
		}
	}
	actor, err := store.Agent(t.Context(), child.ID)
	if err != nil || actor.CurrentRunID != run.ID {
		t.Fatalf("scope update changed the live run: %+v err=%v", actor, err)
	}
	current, err := store.Run(t.Context(), run.ID)
	if err != nil || current.State.Terminal() || current.Version != run.Version {
		t.Fatalf("scope update changed run state: %+v err=%v", current, err)
	}
}

func TestUpdateClaimsRepairsStoppedTaskAndRejectsOverlapWithOtherAgents(t *testing.T) {
	store, _ := openTestStore(t)
	parent, child := registerTestAgents(t, store)
	parentRun, err := store.StartRun(t.Context(), StartRunRequest{AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RequestID: "parent-task", Origin: OriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{AgentID: parent.ID, RunID: parentRun.ID, RequestID: parentRun.RequestID, Paths: []string{"parent-owned"}}); err != nil {
		t.Fatal(err)
	}
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
	result, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: finished.Version, ExpectedVersions: map[string]uint64{}, Paths: []string{"src/one.go", "src/[literal].go"}})
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
	if _, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: finished.Version, ExpectedVersions: versions, Paths: []string{"parent-owned/file.go"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlap with another agent's claim error = %v", err)
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
	first, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: finished.Version, ExpectedVersions: map[string]uint64{}, Paths: []string{"src/one.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateClaims(t.Context(), UpdateClaimsRequest{ParentID: parent.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RunID: run.ID, ExpectedRunVersion: finished.Version, ExpectedVersions: map[string]uint64{}, Paths: []string{"src/two.go"}}); !errors.Is(err, ErrStale) {
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
