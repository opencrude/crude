// Package handover contains the nonsecret contract for an explicitly authorized,
// one-shot goal continuation across core owners. Values grant no authority.
package handover

import (
	"errors"
	"time"

	"crude/types/control"
)

// ErrMaintenancePending is an explicit no-effect continuation refusal: the
// exact permit and all user controls still match, but owned memory work has not
// joined. The core may wait boundedly and recheck that same permit.
var ErrMaintenancePending = errors.New("goal handover is waiting for owned maintenance")

type Phase string

const (
	Reserved    Phase = "reserved"
	Ready       Phase = "ready"
	Consumed    Phase = "consumed"
	Cancelled   Phase = "cancelled"
	Invalidated Phase = "invalidated"
	Expired     Phase = "expired"
)

// Reservation is private owner input. TokenSHA256 is a lowercase SHA-256 hex
// digest; the raw nonce is never retained by the session.
type Reservation struct {
	OperationID          string             `json:"operation_id"`
	GoalID               string             `json:"goal_id"`
	TokenSHA256          string             `json:"token_sha256"`
	SuccessorCohort      string             `json:"successor_cohort"`
	SuccessorSchema      string             `json:"successor_schema"`
	ExpiresAt            time.Time          `json:"expires_at"`
	SourceAgentID        string             `json:"source_agent_id"`
	SourceRunID          string             `json:"source_run_id"`
	SourceRuntimeEpoch   uint64             `json:"source_runtime_epoch"`
	SourceTurnGeneration uint64             `json:"source_turn_generation"`
	SourceCoreOwner      string             `json:"source_core_owner"`
	SourceShell          control.ShellOwner `json:"source_shell"`
}

// State is safe to publish. Deliberately do not embed Reservation: neither a
// nonce nor its digest belongs in observable operation state.
type State struct {
	OperationID          string             `json:"operation_id"`
	GoalID               string             `json:"goal_id"`
	SuccessorCohort      string             `json:"successor_cohort"`
	SuccessorSchema      string             `json:"successor_schema"`
	ExpiresAt            time.Time          `json:"expires_at"`
	SourceAgentID        string             `json:"source_agent_id"`
	SourceRunID          string             `json:"source_run_id"`
	SourceRuntimeEpoch   uint64             `json:"source_runtime_epoch"`
	SourceTurnGeneration uint64             `json:"source_turn_generation"`
	SourceCoreOwner      string             `json:"source_core_owner"`
	SourceShell          control.ShellOwner `json:"source_shell"`
	Phase                Phase              `json:"phase"`
	LifecycleEpoch       uint64             `json:"lifecycle_epoch"`
	ReadyContextRevision uint64             `json:"ready_context_revision"`
	Reason               string             `json:"reason,omitempty"`
}
