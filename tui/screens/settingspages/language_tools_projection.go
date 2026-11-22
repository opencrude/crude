package settingspages

import (
	"path/filepath"
	"sort"
	"strings"

	"crude/insight/catalog"
	"crude/types/insight"
	"crude/types/statuscenter"
)

// languageToolRow joins configurations only to installations with evidence of
// the same command and package. Inventory is project-scoped for external
// executables, so absence from it never means a command is not installed.
type languageToolRow struct {
	id, name, command string
	servers           []lspServer
	managed           *insight.CodeTool
	external          *insight.UnmanagedCodeTool
}

func (r languageToolRow) languages() string {
	names := make([]string, 0, len(r.servers))
	for _, server := range r.servers {
		name := server.id
		if language, ok := catalog.Current().Language(server.id); ok && language.Name != "" {
			name = language.Name
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// projectState describes only the assembly snapshot. ToolReady means that the
// executable was resolved, not that a server is running or has checked files.
func (r languageToolRow) projectState(plan insight.AssemblyPlan) (string, bool) {
	if plan.State != insight.AssemblyReady {
		if plan.State == insight.AssemblyScanning {
			return "Checking", false
		}
		return "Not checked", false
	}
	var state string
	matched := false
	for _, language := range plan.Languages {
		for _, server := range r.servers {
			if server.id != language.ID {
				continue
			}
			// Installed versions are distinct rows. Do not report an unused
			// version as the executable selected by the assembly. A workspace
			// override may select a different command from the visible draft.
			switch {
			case language.Status == insight.ToolReady && r.managed != nil:
				if language.Executable != r.managed.Executable {
					continue
				}
			case language.Status == insight.ToolReady && r.external != nil:
				if language.Executable != r.external.Executable {
					continue
				}
			case server.cfg.Command != language.Command:
				continue
			}
			current := "Not checked"
			switch language.Status {
			case insight.ToolReady:
				current = "Ready"
			case insight.ToolMissing:
				current = "Not installed"
				if r.managed != nil || r.external != nil {
					current = "Not resolved"
				}
			case insight.ToolUnavailable:
				current = "Cannot start"
			case insight.ToolOff:
				current = "Off"
			case insight.ToolNoServer:
				current = "No server"
			}
			if matched && state != current {
				state = "Mixed"
			} else if !matched {
				state = current
			}
			matched = true
			break
		}
	}
	if matched {
		return state, true
	}
	if plan.Limited {
		return "Not checked", false
	}
	return "Not used", false
}

func (r languageToolRow) launchProblem(plan insight.AssemblyPlan) string {
	for _, language := range plan.Languages {
		if language.Status != insight.ToolUnavailable {
			continue
		}
		for _, server := range r.servers {
			if server.id == language.ID && server.cfg.Command == language.Command {
				return language.Reason
			}
		}
	}
	return ""
}

// setupTarget identifies a single candidate without guessing between distinct
// catalog packages that happen to use the same command.
func (r languageToolRow) setupTarget(plan insight.AssemblyPlan) (insight.ProjectToolSetupRequest, bool) {
	if r.managed != nil || r.external != nil || plan.State != insight.AssemblyReady || plan.Limited || plan.Generation == 0 {
		return insight.ProjectToolSetupRequest{}, false
	}
	languageID, packageName := "", ""
	for _, language := range plan.Languages {
		if language.Status != insight.ToolMissing {
			continue
		}
		for _, server := range r.servers {
			if server.id != language.ID || server.cfg.Command != language.Command {
				continue
			}
			if languageID != "" && (language.Package == "" || language.Package != packageName) {
				return insight.ProjectToolSetupRequest{}, false
			}
			if languageID == "" {
				languageID, packageName = language.ID, language.Package
			}
			break
		}
	}
	if languageID == "" {
		return insight.ProjectToolSetupRequest{}, false
	}
	return insight.ProjectToolSetupRequest{Explicit: true, LanguageID: languageID, AssemblyGeneration: plan.Generation}, true
}

func projectUsesExecutable(plan insight.AssemblyPlan, languageID, executable string) bool {
	if plan.State != insight.AssemblyReady || executable == "" {
		return false
	}
	for _, language := range plan.Languages {
		if language.ID == languageID && language.Status == insight.ToolReady && language.Executable == executable {
			return true
		}
	}
	return false
}

func managedMatchesServer(tool insight.CodeTool, server lspServer) bool {
	if server.cfg.Command != "" && server.cfg.Command == tool.Executable {
		return true
	}
	if !server.builtin || tool.Command == "" || tool.Command != server.cfg.Command {
		return false
	}
	c := catalog.Current()
	language, ok := c.Language(server.id)
	if !ok {
		return false
	}
	for _, id := range language.Servers {
		if entry, ok := c.Server(id); ok && entry.Command == tool.Command && entry.Package == tool.Package {
			return true
		}
	}
	return false
}

func externalMatchesServer(tool insight.UnmanagedCodeTool, server lspServer) bool {
	if tool.Command == "" || tool.Command != server.cfg.Command {
		return false
	}
	if !server.builtin {
		return tool.Name == "" || tool.Name == server.id
	}
	language, ok := catalog.Current().Language(server.id)
	if !ok {
		return false
	}
	for _, id := range language.Servers {
		if id == tool.Name {
			return true
		}
	}
	return false
}

func languageToolRows(servers []lspServer, inventory insight.CodeToolInventory) []languageToolRow {
	// One group per exact effective command. A configuration that names no
	// executable remains separate rather than being merged with other blanks.
	groups := make(map[string][]lspServer)
	for _, server := range servers {
		key := server.cfg.Command
		if key == "" {
			key = "\x00" + server.id
		}
		groups[key] = append(groups[key], server)
	}
	matched := make(map[string]bool)
	var rows []languageToolRow
	for _, tool := range inventory.Tools {
		row := languageToolRow{id: "managed/" + tool.ID, name: strings.TrimSpace(tool.Package + " " + tool.Version), command: tool.Command}
		copy := tool
		row.managed = &copy
		for _, server := range servers {
			configurationMatches := managedMatchesServer(tool, server)
			if configurationMatches || projectUsesExecutable(inventory.Project, server.id, tool.Executable) {
				row.servers = append(row.servers, server)
			}
			if configurationMatches {
				matched[server.id] = true
			}
		}
		rows = append(rows, row)
	}
	for _, tool := range inventory.Unmanaged {
		row := languageToolRow{id: "external/" + tool.Executable, name: tool.Command, command: tool.Command}
		if row.name == "" {
			row.name = tool.Name
		}
		copy := tool
		row.external = &copy
		for _, server := range servers {
			configurationMatches := externalMatchesServer(tool, server)
			if configurationMatches || projectUsesExecutable(inventory.Project, server.id, tool.Executable) {
				row.servers = append(row.servers, server)
			}
			if configurationMatches {
				matched[server.id] = true
			}
		}
		rows = append(rows, row)
	}
	for key, configs := range groups {
		// Some configurations in a group can match a different installation.
		// Keep any unmatched ones separately rather than silently dropping them.
		var unmatched []lspServer
		for _, server := range configs {
			if !matched[server.id] {
				unmatched = append(unmatched, server)
			}
		}
		if len(unmatched) > 0 {
			name := unmatched[0].cfg.Command
			if name == "" {
				name = unmatched[0].id
			}
			rows = append(rows, languageToolRow{id: "configured/" + key, name: name, command: unmatched[0].cfg.Command, servers: unmatched})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := strings.ToLower(rows[i].name), strings.ToLower(rows[j].name)
		if a == b {
			return rows[i].id < rows[j].id
		}
		return a < b
	})
	return rows
}

// instances returns the live language-server instances that run this tool.
// Installed tools match their exact executable; a configuration without an
// installation record matches its command name.
func (r languageToolRow) instances(health []statuscenter.LSPServerHealth) []statuscenter.LSPServerHealth {
	executable := ""
	switch {
	case r.managed != nil:
		executable = r.managed.Executable
	case r.external != nil:
		executable = r.external.Executable
	}
	var out []statuscenter.LSPServerHealth
	for _, server := range health {
		switch {
		case executable != "" && filepath.IsAbs(server.Command):
			if server.Command != executable {
				continue
			}
		case r.command == "" || filepath.Base(server.Command) != filepath.Base(r.command):
			continue
		}
		out = append(out, server)
	}
	return out
}

// runtimeState is the row's live state, or "" when no instance exists. A
// failure outranks starting, which outranks running; a tool with no live
// instance but a stopped one reads Stopped.
func runtimeState(instances []statuscenter.LSPServerHealth) string {
	state := ""
	rank := map[string]int{"": 0, "Stopped": 1, "Running": 2, "Starting": 3, "Failed": 4}
	for _, server := range instances {
		current := "Stopped"
		switch {
		case server.Error != "":
			current = "Failed"
		case server.Starting || server.Status == "Waiting for slot" || server.Status == "Stopping":
			current = "Starting"
		case server.Connected:
			current = "Running"
		}
		if rank[current] > rank[state] {
			state = current
		}
	}
	return state
}
