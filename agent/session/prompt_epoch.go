package session

import (
	"slices"
	"strings"

	"crude/agent/hostshell"
	rootprompt "crude/agent/prompt"
	"crude/agent/provider"
	"crude/internal/buildinfo"
	"crude/types/setting"
)

// The base stays on the wire across turns and retries. The effective sections
// include settings already appended to the conversation since that base.
type rootPromptEpoch struct {
	generation       uint64
	baseOptions      rootprompt.RootOptions
	resetUpdates     bool
	base             string
	effectiveOptions rootprompt.RootOptions
	effective        []rootprompt.RootSection
}

func newRootPromptEpoch(options rootprompt.RootOptions, generation uint64) rootPromptEpoch {
	sections := rootprompt.RootSections(options)
	return rootPromptEpoch{generation: generation, baseOptions: options, base: rootprompt.RenderRoot(sections), effective: sections, effectiveOptions: options}
}

// rootOptions takes an immutable effective configuration from the same request
// boundary as tool selection. Identity and the session directory belong to the session.
func (s *Session) rootOptions(cfg *Config, multiAgent, planMode bool, model, root string) rootprompt.RootOptions {
	options := rootprompt.RootOptions{MultiAgent: multiAgent, PlanMode: planMode, Model: model, WorkDir: root, CrudeVersion: buildinfo.Version}
	if s == nil {
		return options
	}
	s.mu.Lock()
	options.WorkspaceAgent = s.selectedMainAgent == CategoryErrand
	options.AgentName = setting.AgentNameForRole(s.selectedMainAgent, s.agentName)
	options.TempDir = s.tempDir
	s.mu.Unlock()
	var shell string
	if cfg != nil {
		shell = cfg.Agents.Shell
		options.ProfileInstructions = cfg.ActiveDefaultCategory().Instructions
		if multiAgent {
			options.AgentCatalog = agentProfileCatalog(cfg)
		}
	}
	options.Shell = hostshell.ResolveAgent(shell)
	return options
}

func (s *Session) rootBase(options rootprompt.RootOptions) string {
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	generation := max(1, s.contextGeneration)
	epoch := &s.promptEpoch
	previous := epoch.baseOptions
	if generation != epoch.generation || options.Model != previous.Model || options.WorkDir != previous.WorkDir || options.WorkspaceAgent != previous.WorkspaceAgent {
		*epoch = newRootPromptEpoch(options, generation)
		// Model switches and passive resume can retain messages from a different
		// system prompt. Preserve that history, but retire its settings overlays.
		epoch.resetUpdates = slices.ContainsFunc(s.activeContext, func(m provider.ChatMessage) bool {
			return m.EffectiveOrigin() == provider.OriginRuntimeContext && strings.HasPrefix(m.InternalID, settingsNoticePrefix)
		})
	}
	return epoch.base
}

const settingsNoticePrefix = "root-settings-"

func (s *Session) rootSettingsUpdate(options rootprompt.RootOptions) *provider.ChatMessage {
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	epoch := &s.promptEpoch
	if epoch.effectiveOptions == options && !epoch.resetUpdates {
		return nil
	}
	current := rootprompt.RootSections(options)
	update := rootprompt.SettingsUpdate(epoch.effective, current, epoch.resetUpdates)
	epoch.effectiveOptions, epoch.effective = options, current
	epoch.resetUpdates = false
	if update == "" {
		return nil
	}
	m := runtimeMessage(update)
	m.InternalID = settingsNoticePrefix + m.InternalID
	return &m
}
