package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Runtime is the admitted neutral operation surface, not a presentation model.
// Implementations resolve descriptors and actual effects before requesting or
// consuming exact authorization. Transport caller fields alone are not grants.
type Runtime interface {
	Describe(context.Context, Caller, Target, string) ([]Descriptor, error)
	Resolve(context.Context, Request) (Descriptor, error)
	Execute(context.Context, Request) (Result, error)
}

type authorizationKey struct{}
type authorization struct {
	token  string
	origin string
}

// ApprovalRequired carries the exact resolved control intent. Human-only
// operations must be rejected before constructing this auto-eligible request.
type ApprovalRequired struct {
	Request    Request
	Descriptor Descriptor
	Token      string
}

func NewApproval(request Request, descriptor Descriptor) *ApprovalRequired {
	raw, _ := json.Marshal(struct {
		Request    Request
		Descriptor Descriptor
	}{request, descriptor})
	digest := sha256.Sum256(raw)
	return &ApprovalRequired{Request: request.Clone(), Descriptor: descriptor.Clone(), Token: hex.EncodeToString(digest[:])}
}

func (a *ApprovalRequired) Error() string {
	return "exact Crude control operation requires authorization"
}

func (a *ApprovalRequired) CapabilityApproval() (string, string) {
	raw, _ := json.Marshal(a.Evidence())
	return a.Request.CommandID, "Authorize this exact Crude operation: " + string(raw)
}

func (a *ApprovalRequired) Evidence() map[string]any {
	return map[string]any{"phase": "control", "token": a.Token, "command": a.Request.CommandID,
		"effect": a.Descriptor.Effect, "target": a.Request.Target, "preconditions": a.Request.Preconditions, "input": a.Request.Input}
}

func (a *ApprovalRequired) CapabilityApprovalContext(ctx context.Context) context.Context {
	return a.CapabilityApprovalContextFor(ctx, "human")
}

func (a *ApprovalRequired) CapabilityApprovalContextFor(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, authorizationKey{}, authorization{token: a.Token, origin: origin})
}

// Authorized checks an internal exact-intent token. It does not make the
// attributed caller current or replace the owner's effect-time fences.
func (a *ApprovalRequired) Authorized(ctx context.Context) (string, bool) {
	value, ok := ctx.Value(authorizationKey{}).(authorization)
	return value.origin, ok && value.token == a.Token && (value.origin == "human" || value.origin == "reviewer" || value.origin == "yolo")
}
