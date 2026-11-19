package types

import (
	"fmt"
	"strings"

	"crude/agent/provider"
)

// MemoryRankerSettings is an optional second model connection, never inherited
// from the coding agent. It accepts a decision model or an ordinary chat model. Workspace overrides inherit individual leaves as usual.
type MemoryRankerSettings struct {
	Enabled      string `json:"enabled,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Model        string `json:"model,omitempty"`
	ModelType    string `json:"model_type,omitempty"`
	EveryActions int    `json:"every_actions,omitempty"`
	TopK         int    `json:"top_k,omitempty"`
}

const MaxRankedMemories = 8

// Model types shared by the memory ranker and Auto Review. Empty is
// Automatic: the account's model list decides, and an unmarked model uses chat.
const (
	ModelTypeAutomatic = provider.ModelTypeAutomatic
	ModelTypeChat      = provider.ModelTypeChat
	ModelTypeDecision  = provider.ModelTypeDecision
)

// ValidModelType reports whether the value is a supported model type.
func ValidModelType(value string) bool {
	return value == ModelTypeAutomatic || value == ModelTypeChat || value == ModelTypeDecision
}

func (r MemoryRankerSettings) WithDefaults() MemoryRankerSettings {
	if r.EveryActions == 0 {
		r.EveryActions = 3
	}
	if r.TopK == 0 {
		r.TopK = 2
	}
	return r
}
func (r MemoryRankerSettings) Validate() error {
	r = r.WithDefaults()
	if r.Enabled != "" && r.Enabled != "on" && r.Enabled != "off" {
		return fmt.Errorf("choose On or Off for memory ranking")
	}
	if r.Enabled == "on" && (r.AccountID == "" || strings.TrimSpace(r.Model) == "") {
		return fmt.Errorf("choose an account and a model for memory ranking")
	}
	if r.EveryActions < 1 {
		return fmt.Errorf("enter at least one action")
	}
	if r.TopK < 1 || r.TopK > MaxRankedMemories {
		return fmt.Errorf("choose between 1 and %d memories", MaxRankedMemories)
	}
	if !ValidModelType(r.ModelType) {
		return fmt.Errorf("choose Automatic, Chat or Decision as the model type")
	}
	return nil
}
