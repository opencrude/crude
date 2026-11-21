package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"crude/agent/provider"
	captypes "crude/types/capability"
)

// RemovedAccountName stands in for an account that a setting still names
// after the account was removed. Crude never shows an account's ID.
const RemovedAccountName = "Removed account"

// AccountName is the name people see for an account: its label, or
// RemovedAccountName when no saved account has this ID. An empty ID has no name.
func AccountName(accounts map[string]provider.Account, id string) string {
	if id == "" {
		return ""
	}
	account, ok := accounts[id]
	if !ok {
		return RemovedAccountName
	}
	if label := strings.TrimSpace(account.Label); label != "" {
		return label
	}
	return "Unnamed account"
}

// AgentRoleName is the display name of a built-in agent role key.
func AgentRoleName(role string) string {
	switch role {
	case "default":
		return "Main Agent"
	case "researcher":
		return "Researcher"
	case "crafter":
		return "Crafter"
	case ErrandRoleID:
		return ErrandName
	case "memory":
		return "Memory Agent"
	}
	return role
}

// accountReference is one setting that names an account: the sentence that
// tells people what removing the account does to it, and the change itself.
// AccountUses and ReleaseAccount read the same list, so a warning can never
// describe a cleanup that does not happen.
type accountReference struct {
	change  string
	release func()
}

// AccountUses lists, in display order, what removing the account changes in
// these settings. Each item is one complete sentence.
func AccountUses(s AppSettings, id string) []string {
	refs := accountReferences(&s, id)
	uses := make([]string, len(refs))
	for i, ref := range refs {
		uses[i] = ref.change
	}
	return uses
}

// AccountRemovalPreview identifies the list shown before removal and the
// selected account. It contains no credentials.
func AccountRemovalPreview(s AppSettings, id string) string {
	data, _ := json.Marshal(append([]string{id, AccountName(s.Accounts, id)}, AccountUses(s, id)...))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReleaseAccount removes every setting reference to the account before the
// account itself is removed. A setting that can inherit goes back to its
// parent's account and model; one that cannot work without an account turns
// off; a search method that uses the account is removed. It reports whether
// anything changed.
func ReleaseAccount(s *AppSettings, id string) bool {
	refs := accountReferences(s, id)
	for _, ref := range refs {
		ref.release()
	}
	return len(refs) > 0
}

func accountReferences(s *AppSettings, id string) []accountReference {
	if s == nil || id == "" {
		return nil
	}
	var refs []accountReference
	add := func(change string, release func()) {
		refs = append(refs, accountReference{change: change, release: release})
	}
	categories := s.Agents.Categories
	for _, role := range agentRoleOrder(categories) {
		if categories[role].AccountID != id {
			continue
		}
		change := AgentRoleName(role) + " will use Main Agent's account and model."
		switch role {
		case "default":
			change = "Main Agent will have no account. Choose a new one in Agents."
		case "memory":
			change = "Memory Agent will use Errand's account and model."
		}
		add(change, func() {
			category := s.Agents.Categories[role]
			category.AccountID, category.Provider, category.Model = "", "", ""
			s.Agents.Categories[role] = category
		})
	}
	profiles := make([]string, 0, len(s.Agents.AgentProfiles))
	for name := range s.Agents.AgentProfiles {
		profiles = append(profiles, name)
	}
	slices.Sort(profiles)
	for _, name := range profiles {
		profile := s.Agents.AgentProfiles[name]
		if profile.AccountID != id {
			continue
		}
		add(fmt.Sprintf("Agent %q will use %s's account and model.", name, AgentRoleName(profile.Category)), func() {
			profile := s.Agents.AgentProfiles[name]
			profile.AccountID, profile.Model = "", ""
			s.Agents.AgentProfiles[name] = profile
		})
	}
	if s.Agents.Suggestions.AccountID == id {
		add("Editor suggestions will use Errand's account and model.", func() {
			// Format, endpoint and speed belong to the removed account's protocol.
			suggestions := &s.Agents.Suggestions
			suggestions.AccountID, suggestions.Model, suggestions.Format, suggestions.EndpointPath, suggestions.ServiceTier = "", "", "", "", ""
		})
	}
	if s.Memory.MemoryCategory.AccountID == id {
		add("Memory Agent will use Errand's account and model.", func() {
			memory := &s.Memory.MemoryCategory
			memory.AccountID, memory.Provider, memory.Model = "", "", ""
		})
	}
	if ranker := s.Memory.Ranker; ranker.AccountID == id {
		change := "Memory ranking will have no account."
		if ranker.Enabled == "on" {
			change = "Memory ranking will turn off."
		}
		add(change, func() {
			ranker := &s.Memory.Ranker
			ranker.AccountID, ranker.Model, ranker.ModelType = "", "", ""
			if ranker.Enabled == "on" {
				ranker.Enabled = "off"
			}
		})
	}
	// Auto Review uses its own account, else only Errand's saved account.
	review := s.Safety.AutoPermission
	named, inherited := review.AccountID == id, review.AccountID == "" && categories[ErrandRoleID].AccountID == id
	if named || inherited && review.Enabled == "on" {
		change := "The Auto Review Agent's account choice will be cleared."
		if review.Enabled == "on" {
			change = "Auto Review Agent will turn off. Crude will ask you instead."
		}
		add(change, func() {
			review := &s.Safety.AutoPermission
			if named {
				review.AccountID, review.Provider, review.Model, review.ModelType = "", "", "", ""
			}
			if review.Enabled == "on" {
				review.Enabled = "off"
			}
		})
	}
	routes := 0
	for _, route := range s.Capabilities.WebSearchRoutes {
		if route.Account == id {
			routes++
		}
	}
	if routes > 0 {
		change := "A search method will be removed."
		if routes > 1 {
			change = fmt.Sprintf("%d search methods will be removed.", routes)
		}
		add(change, func() {
			kept := make([]captypes.WebSearchRoute, 0, len(s.Capabilities.WebSearchRoutes))
			for _, route := range s.Capabilities.WebSearchRoutes {
				if route.Account != id {
					kept = append(kept, route)
				}
			}
			if len(kept) == 0 {
				kept = nil
			}
			s.Capabilities.WebSearchRoutes = kept
		})
	}
	return refs
}

// agentRoleOrder lists the built-in roles in display order, then any other
// stored role keys alphabetically.
func agentRoleOrder(categories map[string]CategorySettings) []string {
	known := []string{"default", "researcher", "crafter", ErrandRoleID, "memory"}
	var others []string
	for role := range categories {
		if !slices.Contains(known, role) {
			others = append(others, role)
		}
	}
	slices.Sort(others)
	return append(known, others...)
}
