package settings

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/types/capability"
)

type CapabilityRequestMsg struct {
	Target   *Model
	Action   capability.Action
	Sequence uint64
	// Supersedes is a still-running live search this request replaces; the
	// host cancels it so only the newest query reaches the network.
	Supersedes uint64
}

// CapabilitySearchTickMsg fires after the live-search quiet period. Only the
// newest generation for its exact settings model may start a search.
type CapabilitySearchTickMsg struct {
	Target     *Model
	Generation uint64
}

type CapabilityResultMsg struct {
	Target              *Model
	Snapshot            capability.Snapshot
	Entries             []capability.Entry
	Content             string
	Action              capability.Action
	Err                 error
	Sequence            uint64
	WorkspaceGeneration uint64
}

var nextCapabilityRequest atomic.Uint64

func (m *Model) SetCapabilitySnapshot(snapshot capability.Snapshot) {
	if snapshot.Generation < m.capabilitySnapshot.Generation {
		return
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	var detached capability.Snapshot
	if json.Unmarshal(encoded, &detached) != nil {
		return
	}
	m.capabilitySnapshot = detached
	settings := capability.Normalize(detached.Settings)
	if m.capabilityScope() == capability.ScopeApplication {
		for id, skill := range settings.Skills {
			if skill.Scope == capability.ScopeWorkspace {
				delete(settings.Skills, id)
				if inherited, exists := m.appDefaults.Capabilities.Skills[id]; exists {
					settings.Skills[id] = inherited
				}
			}
		}
		for id, connection := range settings.Connections {
			if connection.Scope == capability.ScopeWorkspace {
				delete(settings.Connections, id)
				if inherited, exists := m.appDefaults.Capabilities.Connections[id]; exists {
					settings.Connections[id] = inherited
				}
			}
		}
		for id, source := range settings.Sources {
			if source.Scope == capability.ScopeWorkspace {
				delete(settings.Sources, id)
				if inherited, exists := m.appDefaults.Capabilities.Sources[id]; exists {
					settings.Sources[id] = inherited
				}
			}
		}
	}
	m.capabilities.Skills = settings.Skills
	m.capabilities.Connections = settings.Connections
	m.capabilities.Sources = settings.Sources
	m.capabilityRevision++
}

func (m *Model) applyCapabilityResult(result CapabilityResultMsg) {
	action, exists := m.capabilityRequests[result.Sequence]
	if result.Target != m || result.Sequence == 0 || !exists {
		return
	}
	delete(m.capabilityRequests, result.Sequence)
	result.Action = action
	if result.Snapshot.Generation >= m.capabilitySnapshot.Generation && result.Snapshot.Generation != 0 {
		m.SetCapabilitySnapshot(result.Snapshot)
	}
	if (result.Err == nil || len(result.Entries) > 0) && (action.Operation == "search" || action.Operation == "discover") {
		m.capabilityDiscovery[action.Kind] = slices.Clone(result.Entries)
		if m.capabilityDiscoveryQuery == nil {
			m.capabilityDiscoveryQuery = make(map[capability.Kind]capability.Action)
		}
		m.capabilityDiscoveryQuery[action.Kind] = action
		m.capabilityRevision++
	}
	if action.Operation == "snapshot" && result.Sequence != m.capabilitySequence && m.capabilityPending != nil {
		return
	}
	if result.Sequence == m.capabilitySequence {
		m.capabilityPending = nil
	}
	m.capabilityResult = &result
}

func (m *Model) requestCapability(action capability.Action) (uint64, tea.Cmd) {
	auxiliary := action.Operation == "snapshot" || action.Operation == "auth-return" || action.Operation == "open-auth-url"
	// A newer live search replaces an older one instead of queueing behind it.
	supersedes := uint64(0)
	if m.capabilityPending != nil && m.capabilityPending.Operation == "discover" && action.Operation == "discover" {
		supersedes = m.capabilitySequence
		delete(m.capabilityRequests, supersedes)
		m.capabilityPending = nil
	}
	if m.capabilityPending != nil && action.Operation != "cancel" && !auxiliary {
		return 0, nil
	}
	encoded, err := json.Marshal(action)
	if err != nil {
		return 0, nil
	}
	var detached capability.Action
	if json.Unmarshal(encoded, &detached) != nil {
		return 0, nil
	}
	detached.Scope = m.capabilityScope()
	sequence := nextCapabilityRequest.Add(1)
	if m.capabilityRequests == nil {
		m.capabilityRequests = make(map[uint64]capability.Action)
	}
	if action.Operation == "cancel" && m.capabilityPending != nil {
		detached.ID, detached.Kind = m.capabilityPending.ID, m.capabilityPending.Kind
		detached.Arguments = map[string]string{"sequence": strconv.FormatUint(m.capabilitySequence, 10)}
		clear(m.capabilityRequests)
	}
	m.capabilityRequests[sequence] = detached
	if !auxiliary || m.capabilityPending == nil {
		m.capabilitySequence = sequence
		m.capabilityPending = &detached
		m.capabilityResult = nil
	}
	return sequence, func() tea.Msg {
		return CapabilityRequestMsg{Target: m, Action: detached, Sequence: sequence, Supersedes: supersedes}
	}
}

func (m *Model) capabilityScope() capability.Scope {
	if string(m.Scope()) == string(capability.ScopeWorkspace) {
		return capability.ScopeWorkspace
	}
	return capability.ScopeApplication
}

func (m *Model) buildAcquisitionForm() *ui.FormModel {
	settings := m.capabilities.Acquisition
	selection := ui.SegmentedFieldState("capability-automatic-selection", "Use installed", []ui.FormOption{{ID: "on", Label: "When relevant"}, {ID: "off", Label: "Only when asked"}}, settings.AutomaticSelection,
		"Use relevant installed capabilities; never installs or grants new access.")
	mode := ui.SegmentedFieldState("capability-acquisition-mode", "Install new", []ui.FormOption{{ID: "manual", Label: "Never"}, {ID: "ask", Label: "Ask first"}, {ID: "automatic", Label: "Within approved sources"}}, settings.Mode,
		"Agent installs only; manual installs still work. No execution access granted.")
	return formModel(
		ui.SeparatorFieldState("capability-selection", "Selection and approval"), selection, mode,
		&ui.FieldState{ID: "capability-sources", Label: "Sources and trust", Type: ui.FieldNav, Target: "settings.capability.sources", Columns: []string{"Registries and repositories"}, Description: "Where Skills and MCP search, and which sources are trusted."},
		ui.SeparatorFieldState("capability-budgets", "Operation and request budgets"),
		capabilityIntField("capability-timeout", "Install timeout", "seconds", int64(settings.TimeoutSeconds), 1, 3600, "A bounded acquisition deadline, not permission to retry a remote change."),
		capabilityIntField("capability-download-bytes", "Largest download", "MB", max(1, settings.MaxDownloadBytes/mebibyte), 1, 1024, "Maximum download size; artifacts are verified before use."),
		capabilityIntField("capability-max-tools", "Tools per request", "max", int64(settings.MaxTools), 1, 1024, "Maximum dynamic tools offered per request; selections are retained."),
		capabilityIntField("capability-schema-bytes", "Schema budget", "KB", int64(settings.MaxSchemaBytes)/1024, 1, 16<<10, "Total size of dynamic tool schemas in one request."),
	)
}

func capabilityIntField(id, label, unit string, value, minimum, maximum int64, description string) *ui.FieldState {
	field := ui.TextFieldState(id, label, strconv.FormatInt(value, 10), description)
	field.Unit = unit
	field.Validator = func(value ui.FieldValue) []string {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value.String()), 10, 64)
		if err != nil || parsed < minimum || parsed > maximum {
			return []string{fmt.Sprintf("Enter a whole number from %d to %d.", minimum, maximum)}
		}
		return nil
	}
	return field
}

func (m *Model) acquisitionFromForm() capability.AcquisitionSettings {
	settings := m.capabilities.Acquisition
	if len(m.categories) <= catAcquisition {
		return settings
	}
	settings.AutomaticSelection = m.value(catAcquisition, "capability-automatic-selection")
	settings.Mode = m.value(catAcquisition, "capability-acquisition-mode")
	if value, err := strconv.Atoi(m.value(catAcquisition, "capability-timeout")); err == nil {
		settings.TimeoutSeconds = value
	}
	// Displayed sizes are rounded; an untouched value keeps its exact bytes.
	if value, err := strconv.ParseInt(m.value(catAcquisition, "capability-download-bytes"), 10, 64); err == nil && value != max(1, settings.MaxDownloadBytes/mebibyte) {
		settings.MaxDownloadBytes = value * mebibyte
	}
	if value, err := strconv.Atoi(m.value(catAcquisition, "capability-max-tools")); err == nil {
		settings.MaxTools = value
	}
	if value, err := strconv.Atoi(m.value(catAcquisition, "capability-schema-bytes")); err == nil && value != settings.MaxSchemaBytes/1024 {
		settings.MaxSchemaBytes = value * 1024
	}
	return settings
}

func (m *Model) editorOpen() bool {
	return m.accountEditor != nil || m.capabilityDraft != nil || len(m.categories) > catAgents && m.categories[catAgents].Form.FieldCount() > 0
}

func (m *Model) categoryMatches(index int, category Category, query string) bool {
	if strings.Contains(strings.ToLower(category.Name+" "+category.Group+" "+category.Description), query) {
		return true
	}
	for _, field := range category.Form.Fields() {
		if strings.Contains(strings.ToLower(field.ID+" "+field.Label+" "+field.Description), query) {
			return true
		}
		for _, option := range field.Options {
			if strings.Contains(strings.ToLower(option.ID+" "+option.Label), query) {
				return true
			}
		}
	}
	var entries []string
	switch index {
	case catSkills:
		entries = append(entries, "skill-id skill-name skill-path invocation mode instructions files access versions")
		for id, skill := range m.capabilities.Skills {
			entries = append(entries, id+" "+skill.Name+" "+skill.Source+" "+skill.Path)
		}
	case catMCP:
		entries = append(entries, "mcp-id mcp-name mcp-url mcp-command transport environment secret references headers authentication timeout tools resources prompts grants logs")
		for id, connection := range m.capabilities.Connections {
			entries = append(entries, id+" "+connection.Name+" "+connection.URL+" "+connection.Command+" "+connection.Source)
		}
	case catSources:
		entries = append(entries, "source-id source-name source-kind source-location source-revision source-digest trusted disabled")
		for id, source := range m.capabilities.Sources {
			entries = append(entries, id+" "+source.Name+" "+source.Kind+" "+source.Location)
		}
	}
	return strings.Contains(strings.ToLower(strings.Join(entries, " ")), query)
}

func formDraftBytes(form *ui.FormModel) []byte {
	values := make(map[string]string)
	if form != nil {
		for _, field := range form.Fields() {
			if !field.ReadOnly && field.Type != ui.FieldSeparator && field.Type != ui.FieldSpacer && field.Type != ui.FieldNav {
				values[field.ID] = field.Value.String()
			}
		}
	}
	encoded, _ := json.Marshal(values)
	return encoded
}
