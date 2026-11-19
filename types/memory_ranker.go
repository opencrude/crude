package types

import (
	"fmt"
	"strings"
)

// MemoryRankerSettings is an optional second model connection, never inherited
// from the coding agent. Workspace overrides inherit individual leaves as usual.
type MemoryRankerSettings struct {
	Enabled      string `json:"enabled,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Model        string `json:"model,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
	EveryActions int    `json:"every_actions,omitempty"`
	TopK         int    `json:"top_k,omitempty"`
}

const MaxRankedMemories = 8

const MemoryRankerChat = "chat"

func (r MemoryRankerSettings) WithDefaults() MemoryRankerSettings {
	if r.EveryActions == 0 {
		r.EveryActions = 3
	}
	if r.TopK == 0 {
		r.TopK = 2
	}
	if r.Endpoint == "" {
		r.Endpoint = "systemone"
	}
	return r
}
func (r MemoryRankerSettings) Validate() error {
	r = r.WithDefaults()
	if r.Enabled != "" && r.Enabled != "on" && r.Enabled != "off" {
		return fmt.Errorf("choose On or Off for memory ranking")
	}
	if r.Enabled == "on" && (r.AccountID == "" || strings.TrimSpace(r.Model) == "") {
		return fmt.Errorf("choose an account and enter a decision model ID")
	}
	if r.EveryActions < 1 {
		return fmt.Errorf("enter at least one action")
	}
	if r.TopK < 1 || r.TopK > MaxRankedMemories {
		return fmt.Errorf("choose between 1 and %d memories", MaxRankedMemories)
	}
	if r.Endpoint != "systemone" && r.Endpoint != "decisions" && r.Endpoint != MemoryRankerChat {
		return fmt.Errorf("choose a supported decision endpoint")
	}
	return nil
}
