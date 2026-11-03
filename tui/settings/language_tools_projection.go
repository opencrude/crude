package settings

import (
	"sort"
	"strings"

	"crude/insight/catalog"
	"crude/types"
)

// languageToolRow joins configurations only to installations with evidence of
// the same command and package. Inventory is project-scoped for external
// executables, so absence from it never means a command is not installed.
type languageToolRow struct {
	id, name, command string
	servers           []lspServer
	managed           *types.CodeTool
	external          *types.UnmanagedCodeTool
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
func (r languageToolRow) projectState(plan types.AssemblyPlan) (string, bool) {
	if plan.State != types.AssemblyReady {
		if plan.State == types.AssemblyScanning {
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
			case language.Status == types.ToolReady && r.managed != nil:
				if language.Executable != r.managed.Executable {
					continue
				}
			case language.Status == types.ToolReady && r.external != nil:
				if language.Executable != r.external.Executable {
					continue
				}
			case server.cfg.Command != language.Command:
				continue
			}
			current := "Not checked"
			switch language.Status {
			case types.ToolReady:
				current = "Ready"
			case types.ToolMissing:
				current = "Not installed"
				if r.managed != nil || r.external != nil {
					current = "Not resolved"
				}
			case types.ToolOff:
				current = "Off"
			case types.ToolNoServer:
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

// setupTarget identifies a single candidate without guessing between distinct
// catalog packages that happen to use the same command.
func (r languageToolRow) setupTarget(plan types.AssemblyPlan) (types.ProjectToolSetupRequest, bool) {
	if r.managed != nil || r.external != nil || plan.State != types.AssemblyReady || plan.Limited || plan.Generation == 0 {
		return types.ProjectToolSetupRequest{}, false
	}
	languageID, packageName := "", ""
	for _, language := range plan.Languages {
		if language.Status != types.ToolMissing {
			continue
		}
		for _, server := range r.servers {
			if server.id != language.ID || server.cfg.Command != language.Command {
				continue
			}
			if languageID != "" && (language.Package == "" || language.Package != packageName) {
				return types.ProjectToolSetupRequest{}, false
			}
			if languageID == "" {
				languageID, packageName = language.ID, language.Package
			}
			break
		}
	}
	if languageID == "" {
		return types.ProjectToolSetupRequest{}, false
	}
	return types.ProjectToolSetupRequest{Explicit: true, LanguageID: languageID, AssemblyGeneration: plan.Generation}, true
}

func projectUsesExecutable(plan types.AssemblyPlan, languageID, executable string) bool {
	if plan.State != types.AssemblyReady || executable == "" {
		return false
	}
	for _, language := range plan.Languages {
		if language.ID == languageID && language.Status == types.ToolReady && language.Executable == executable {
			return true
		}
	}
	return false
}

func managedMatchesServer(tool types.CodeTool, server lspServer) bool {
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

func externalMatchesServer(tool types.UnmanagedCodeTool, server lspServer) bool {
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

func languageToolRows(servers []lspServer, inventory types.CodeToolInventory) []languageToolRow {
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
