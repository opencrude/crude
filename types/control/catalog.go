package control

import (
	"crude/worktree"
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// StructSchema derives only exported JSON data fields, never methods. Domain
// owners bind their actual RPC DTOs at startup to avoid a protocol import cycle.
func StructSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if t.PkgPath() == "time" {
			return map[string]any{"type": "string", "format": "date-time"}
		}
		props := map[string]any{}
		var required []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			name := tag[0]
			if f.PkgPath != "" || name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			props[name] = StructSchema(f.Type)
			if !strings.Contains(f.Tag.Get("json"), "omitempty") {
				required = append(required, name)
			}
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": StructSchema(t.Elem())}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		return map[string]any{"type": "array", "items": StructSchema(t.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "minimum": 0}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Interface:
		return map[string]any{}
	default:
		return map[string]any{"type": "string"}
	}
}

// Catalog is bounded discovery, not an execution grant. Schemas describe the
// semantic API, not arbitrary core RPC methods. Lookup returns detached values.
// A query matches by its words. Commands containing every word are returned
// when any exist (terms in the command ID rank first); otherwise commands are
// ranked by how many words they contain, so a natural phrase still finds the
// right command instead of requiring one exact substring.
func Catalog(query string) []Descriptor {
	terms := catalogTerms(query)
	type ranked struct {
		descriptor Descriptor
		matched    int
		score      int
	}
	matches := make([]ranked, 0)
	complete := false
	for _, d := range commandCatalog {
		match := ranked{descriptor: d}
		if len(terms) > 0 {
			id, text := strings.ToLower(d.ID), strings.ToLower(d.ID+" "+string(d.Input.Document))
			for _, term := range terms {
				if strings.Contains(text, term) {
					match.matched++
					match.score++
					if strings.Contains(id, term) {
						match.score++
					}
				}
			}
			if match.matched == 0 {
				continue
			}
		}
		complete = complete || match.matched == len(terms)
		matches = append(matches, match)
	}
	if complete {
		matches = slices.DeleteFunc(matches, func(match ranked) bool { return match.matched < len(terms) })
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].matched != matches[j].matched {
			return matches[i].matched > matches[j].matched
		}
		return matches[i].score > matches[j].score
	})
	out := make([]Descriptor, 0, min(len(matches), MaxPageLimit))
	for _, match := range matches[:min(len(matches), MaxPageLimit)] {
		out = append(out, match.descriptor.Clone())
	}
	return out
}

// catalogTerms splits a query into words and reduces simple plurals, so
// "capability" finds "capabilities" and "secrets" finds "secret".
func catalogTerms(query string) []string {
	var terms []string
	for _, word := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-'
	}) {
		switch {
		case strings.HasSuffix(word, "ies") && len(word) > 4:
			word = strings.TrimSuffix(word, "ies")
		case strings.HasSuffix(word, "y") && len(word) > 3:
			word = strings.TrimSuffix(word, "y")
		case strings.HasSuffix(word, "s") && len(word) > 3:
			word = strings.TrimSuffix(word, "s")
		}
		if len(word) > 1 {
			terms = append(terms, word)
		}
	}
	return terms
}

func Lookup(id string) (Descriptor, bool) {
	for _, d := range commandCatalog {
		if d.ID == id {
			return d.Clone(), true
		}
	}
	return Descriptor{}, false
}

func catalogCommand(id string, scope Scope, effect Effect, description, properties string, required ...string) Descriptor {
	var props map[string]any
	if err := json.Unmarshal([]byte(properties), &props); err != nil {
		panic(err)
	}
	doc, _ := json.Marshal(map[string]any{"type": "object", "description": description, "properties": props, "required": required, "additionalProperties": false})
	return Descriptor{ID: id, Scope: scope, Effect: effect,
		Input:        Schema{ID: id + ".input", Revision: 1, Document: doc},
		Output:       Schema{ID: id + ".output", Revision: 1, Document: json.RawMessage(`{"description":"Exact owner result; failures use the control result fault.","type":["object","array"]}`)},
		Availability: Availability{Available: true}}
}

var commandCatalog = func() []Descriptor {
	var out []Descriptor
	add := func(id string, scope Scope, effect Effect, description, properties string, required ...string) {
		out = append(out, catalogCommand(id, scope, effect, description, properties, required...))
	}
	add("settings.get", Domain, Read, "Read redacted settings and exact revisions. Session scope requires target.session_id.", `{"scope":{"type":"string","enum":["application","workspace","session"],"description":"Defaults to workspace."}}`)
	add("settings.fields", Domain, Read, "Discover exact ordinary JSON field paths, redacted values, schemas, ownership and revisions. Echo cursor for the next bounded page.", `{"scope":{"type":"string","enum":["application","workspace","session"]},"prefix":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":128},"cursor":{"type":"string"}}`, "scope", "limit")
	add("capabilities.inspect", Domain, Read, "List MCP connections (state, usable and switched-off tools, built-in provider keys stored or unset), skills and stored secret names. Secret values are never returned.", `{}`)
	add("capabilities.set_secret", Domain, Write, "Store a credential in Crude's secret store for the user, e.g. a search API key they gave you (EXA_API_KEY, BRAVE_SEARCH_API_KEY, TAVILY_API_KEY, SEARXNG_URL, JINA_API_KEY for the built-in web tools). Connections reference it as secret:NAME and reconnect to use it. Never echo the value back.", `{"name":{"type":"string","description":"Secret name, e.g. EXA_API_KEY"},"secret":{"type":"string","description":"The credential value (write-only)"}}`, "name", "secret")
	add("capabilities.remove_secret", Domain, Delete, "Delete a stored secret by name.", `{"name":{"type":"string"}}`, "name")
	add("capabilities.set_enabled", Domain, Write, "Enable or disable an installed MCP connection (including built-ins such as web) for every session.", `{"id":{"type":"string"},"enabled":{"type":"boolean"}}`, "id", "enabled")
	add("capabilities.set_tool_enabled", Domain, Write, "Switch one tool of an installed MCP connection on or off. Enabling an external server's tool for the first time uses mcp_request_access instead.", `{"id":{"type":"string"},"tool":{"type":"string","description":"The server's tool name, e.g. arxiv_search"},"enabled":{"type":"boolean"}}`, "id", "tool", "enabled")
	add("capabilities.save_connection", Domain, Execute, "Create or edit an MCP connection. Omitted fields keep their stored values. transport is http (Streamable HTTP; falls back to legacy SSE automatically), sse, or stdio (a local process). Put credentials in secret_refs as secret:NAME after capabilities.set_secret, not in headers or environment.", `{"connection":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string"},"name":{"type":"string"},"transport":{"type":"string","enum":["http","sse","stdio"]},"url":{"type":"string"},"command":{"type":"string"},"args":{"type":"array","items":{"type":"string"}},"directory":{"type":"string"},"environment":{"type":"object","additionalProperties":{"type":"string"}},"headers":{"type":"object","additionalProperties":{"type":"string"}},"secret_refs":{"type":"object","additionalProperties":{"type":"string"},"description":"Header or variable name to secret:NAME or env:NAME"},"auth":{"type":"string","enum":["none","oauth"]},"timeout_seconds":{"type":"integer","minimum":1,"maximum":3600},"launch_approved":{"type":"boolean"},"enabled":{"type":"boolean"}},"required":["id"]}}`, "connection")
	add("capabilities.remove_connection", Domain, Delete, "Remove an installed MCP connection. Built-in connections can only be disabled.", `{"id":{"type":"string"}}`, "id")
	add("execution.selection", Domain, Read, "Read the exact session's persisted next-input execution selection. Requires target.session_id; no resource target.", `{}`)
	out[len(out)-1].Requires.Session = true
	add("execution.select", Domain, Write, "Select execution for future input in target.session_id; never moves live runs or queued input. Resource target must equal id, including empty id for shared execution. expected_revision fences the persisted session selection.", `{"id":{"type":"string","description":"Exact ready execution ID, or empty for shared logical workspace."},"expected_revision":{"type":"integer","minimum":0}}`, "id", "expected_revision")
	out[len(out)-1].Requires.Session = true
	for _, id := range []string{"execution.list", "execution.integrations", "execution.create", "execution.inspect", "execution.review", "execution.apply", "execution.keep", "execution.cleanup", "execution.undo", "execution.recover"} {
		effect := Read
		props := `{}`
		required := []string(nil)
		switch id {
		case "execution.create":
			effect = Execute
			props = `{"mode":{"type":"string","enum":["auto","shared","worktree"]},"base_commit":{"type":"string"},"independent":{"type":"boolean"},"include_ignored":{"type":"array","items":{"type":"string"}}}`
			required = []string{"mode"}
		case "execution.apply":
			raw, _ := json.Marshal(map[string]any{"plan": StructSchema(reflect.TypeOf(worktree.PlanDelta{}))})
			props, required, effect = string(raw), []string{"plan"}, Write
		case "execution.undo", "execution.recover":
			props, required, effect = `{"operation_id":{"type":"string"}}`, []string{"operation_id"}, Write
		case "execution.inspect", "execution.review", "execution.keep", "execution.cleanup":
			props, required = `{"id":{"type":"string"}}`, []string{"id"}
			if id == "execution.keep" {
				effect = Write
			}
			if id == "execution.cleanup" {
				effect = Delete
			}
		}
		add(id, Domain, effect, "Managed execution through accepted documents and Local History. Exact resource target must match id, plan.context_id or operation_id. Owner is gateway attribution, never input authority.", props, required...)
	}
	for _, id := range []string{"settings.set", "settings.reset"} {
		props := `{"scope":{"type":"string","enum":["application","workspace","session"]},"path":{"type":"string","description":"Exact dot-separated JSON field path. Reset removes only this field's override; application reset uses shipped defaults."}`
		required := []string{"scope", "path"}
		if id == "settings.set" {
			props += `,"value":{"description":"JSON value of the field's exact current Go JSON type; unknown nested fields are rejected."}`
			required = append(required, "value")
		}
		add(id, Domain, Write, "Explicit single-field settings operation: last write wins; unspecified fields inherit. Authority and account entities require human-owned operations; MCP connections and their secrets use the capabilities.* commands.", props+`}`, required...)
	}
	add("session.list", Domain, Read, "List saved sessions without starting model work.", `{}`)
	add("operation.receipt", Domain, Read, "Observe the original domain or shell mutation receipt owned by this exact caller. Never replay uncertain effects.", `{"request_id":{"type":"string"}}`, "request_id")
	add("session.create", Domain, Execute, "Create a session through the core lifecycle owner; does not submit input.", `{}`)
	add("session.open", Domain, Execute, "Passively open the exact saved session through the lifecycle owner.", `{"session_id":{"type":"string"}}`, "session_id")
	add("document.open", Domain, Read, "Open a shared document view for the trusted caller owner.", `{"path":{"type":"string"}}`, "path")
	add("document.read", Domain, Read, "Read authoritative shared text and revision.", `{"id":{"type":"string"}}`, "id")
	add("document.edit", Domain, Write, "Apply exact revision-fenced edits as one author undo group.", `{"edits":{"items":{"additionalProperties":false,"properties":{"End":{"type":"integer"},"Start":{"type":"integer"},"Text":{"type":"string"}},"required":["Start","End","Text"],"type":"object"},"type":"array"},"group":{"type":"string"},"id":{"type":"string"},"revision":{"minimum":0,"type":"integer"}}`, "id", "revision", "edits")
	for _, id := range []string{"document.save", "document.undo", "document.redo", "document.format"} {
		add(id, Domain, Write, "Use the shared document's exact current revision and trusted author owner.", `{"id":{"type":"string"},"revision":{"type":"integer","minimum":0},"reason":{"type":"string"}}`, "id", "revision")
	}
	add("terminal.list", Domain, Read, "List retained terminals.", `{}`)
	add("terminal.create", Domain, Execute, "Create a retained terminal using the exact typed launch options.", `{"Args":{"items":{"type":"string"},"type":"array"},"CWD":{"type":"string"},"Cols":{"type":"integer"},"Command":{"type":"string"},"Rows":{"type":"integer"},"Title":{"type":"string"}}`)
	add("terminal.rows", Domain, Read, "Read a bounded retained terminal row range.", `{"count":{"type":"integer","minimum":1,"maximum":128},"id":{"type":"string"},"start":{"type":"integer"}}`, "id", "start", "count")
	add("terminal.control", Domain, Write, "Acquire an exact controller lease for the trusted caller.", `{"id":{"type":"string"}}`, "id")
	add("terminal.input", Domain, Execute, "Send input using the exact current lease. Never infer a shell/controller.", `{"data":{"contentEncoding":"base64","type":"string"},"focused":{"type":"boolean"},"key":{"additionalProperties":false,"properties":{"Code":{"type":"integer"},"Mod":{"minimum":0,"type":"integer"},"Text":{"type":"string"}},"required":["Code","Text","Mod"],"type":"object"},"kind":{"type":"string"},"lease":{"additionalProperties":false,"properties":{"Generation":{"minimum":0,"type":"integer"},"Owner":{"additionalProperties":false,"properties":{"ClientID":{"type":"string"},"Epoch":{"minimum":0,"type":"integer"}},"required":["ClientID","Epoch"],"type":"object"},"TerminalID":{"type":"string"}},"required":["TerminalID","Owner","Generation"],"type":"object"},"mouse":{"additionalProperties":false,"properties":{"Action":{"minimum":0,"type":"integer"},"Button":{"minimum":0,"type":"integer"},"Mod":{"minimum":0,"type":"integer"},"X":{"type":"integer"},"Y":{"type":"integer"}},"required":["Action","Button","X","Y","Mod"],"type":"object"}}`, "lease", "kind")
	add("terminal.close", Domain, Delete, "Close the exact retained terminal and its PTY by id (from terminal.open/terminal.create resource_ids or terminal.list); every shell unmounts it.", `{"id":{"type":"string"}}`, "id")
	add("files.list", Domain, Read, "List one workspace directory; rejects symlink/escaping paths.", `{"new_path":{"type":"string"},"path":{"type":"string"}}`, "path")
	add("files.read", Domain, Read, "Read a file chunk; continuation must echo the exact digest.", `{"digest":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":32768},"offset":{"type":"integer"},"path":{"type":"string"}}`, "path", "offset", "limit")
	add("files.search", Domain, Read, "Search workspace files with the typed project search options.", `{"options":{"additionalProperties":false,"properties":{"CaseSensitive":{"type":"boolean"},"IncludeHidden":{"type":"boolean"},"Regex":{"type":"boolean"},"WholeWord":{"type":"boolean"}},"required":["CaseSensitive","Regex","WholeWord","IncludeHidden"],"type":"object"},"query":{"type":"string"}}`, "query", "options")
	for _, id := range []string{"files.create", "files.rename", "files.delete"} {
		effect := Write
		if id == "files.delete" {
			effect = Delete
		}
		add(id, Domain, effect, "Workspace file operation through shared document and Local History owners.", `{"path":{"type":"string"},"new_path":{"type":"string"}}`, "path")
	}
	add("git.snapshot", Domain, Read, "Read the core Git snapshot.", `{}`)
	add("git.review", Domain, Read, "Compare the exact selected Git change before mutation.", `{"change":{"additionalProperties":false,"properties":{"AbsolutePath":{"type":"string"},"Area":{"minimum":0,"type":"integer"},"Code":{"type":"string"},"Description":{"type":"string"},"Path":{"type":"string"}},"required":["Path","AbsolutePath","Area","Code","Description"],"type":"object"}}`, "change")
	add("git.stage_reviewed", Domain, Write, "Stage only the reviewed comparison with current preimage checks.", `{"comparison":{"additionalProperties":false,"properties":{"AbsolutePath":{"type":"string"},"After":{"type":"string"},"AfterMode":{"minimum":0,"type":"integer"},"AfterPresent":{"type":"boolean"},"Area":{"minimum":0,"type":"integer"},"BaseLabel":{"type":"string"},"Before":{"type":"string"},"BeforeMode":{"minimum":0,"type":"integer"},"BeforePresent":{"type":"boolean"},"Binary":{"type":"boolean"},"Deleted":{"type":"boolean"},"Fingerprint":{"type":"string"},"Historical":{"type":"boolean"},"OriginalPath":{"type":"string"},"Path":{"type":"string"},"RepositoryRoot":{"type":"string"},"TargetLabel":{"type":"string"},"Untracked":{"type":"boolean"}},"required":["Path","AbsolutePath","RepositoryRoot","OriginalPath","BaseLabel","TargetLabel","Before","After","Fingerprint","Area","Binary","Untracked","Deleted","BeforePresent","AfterPresent","BeforeMode","AfterMode","Historical"],"type":"object"}}`, "comparison")
	add("git.discard_preview", Domain, Read, "Preview exact discard consequences.", `{"change":{"additionalProperties":false,"properties":{"AbsolutePath":{"type":"string"},"Area":{"minimum":0,"type":"integer"},"Code":{"type":"string"},"Description":{"type":"string"},"Path":{"type":"string"}},"required":["Path","AbsolutePath","Area","Code","Description"],"type":"object"}}`, "change")
	add("git.discard_reviewed", Domain, Delete, "Apply the exact reviewed discard with mandatory safety history.", `{"preview":{"additionalProperties":false,"properties":{"Comparison":{"additionalProperties":false,"properties":{"AbsolutePath":{"type":"string"},"After":{"type":"string"},"AfterMode":{"minimum":0,"type":"integer"},"AfterPresent":{"type":"boolean"},"Area":{"minimum":0,"type":"integer"},"BaseLabel":{"type":"string"},"Before":{"type":"string"},"BeforeMode":{"minimum":0,"type":"integer"},"BeforePresent":{"type":"boolean"},"Binary":{"type":"boolean"},"Deleted":{"type":"boolean"},"Fingerprint":{"type":"string"},"Historical":{"type":"boolean"},"OriginalPath":{"type":"string"},"Path":{"type":"string"},"RepositoryRoot":{"type":"string"},"TargetLabel":{"type":"string"},"Untracked":{"type":"boolean"}},"required":["Path","AbsolutePath","RepositoryRoot","OriginalPath","BaseLabel","TargetLabel","Before","After","Fingerprint","Area","Binary","Untracked","Deleted","BeforePresent","AfterPresent","BeforeMode","AfterMode","Historical"],"type":"object"}},"required":["Comparison"],"type":"object"}}`, "preview")
	add("history.timeline", Domain, Read, "Read bounded Local History entries.", `{"limit":{"type":"integer","minimum":1,"maximum":32768},"path":{"type":"string"}}`, "limit")
	add("history.plan", Domain, Read, "Build an immutable restore preview for a selected snapshot.", `{"agent_id":{"type":"string"},"message_id":{"type":"string"},"options":{"additionalProperties":false,"properties":{"disable_gitignore":{"type":"boolean"},"extra_ignore_patterns":{"items":{"type":"string"},"type":"array"},"include_paths":{"items":{"type":"string"},"type":"array"},"kind":{"type":"string"},"label":{"type":"string"},"max_file_size":{"type":"integer"},"parent_id":{"type":"string"},"use_gitignore":{"type":"boolean"}},"required":null,"type":"object"},"paths":{"items":{"type":"string"},"type":"array"},"session_id":{"type":"string"},"snapshot_id":{"type":"string"}}`, "snapshot_id", "options")
	add("history.apply", Domain, Write, "Apply the exact immutable restore plan and explicit conflict choices.", `{"choices":{"items":{"additionalProperties":false,"properties":{"Exact":{"type":"boolean"},"Path":{"type":"string"}},"required":["Path","Exact"],"type":"object"},"type":"array"},"plan_id":{"type":"string"}}`, "plan_id", "choices")
	for _, s := range []struct {
		id, desc, props string
		required        []string
	}{
		{"shell.inspect", "Inspect bounded shell topology without private drafts.", `{}`, nil},
		{"shell.controls", "Inspect bounded semantic control nodes.", `{"cursor":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":128}}`, []string{"limit"}},
		{"tree.set_visible", "Set this shell's tree visibility.", `{"visible":{"type":"boolean"}}`, []string{"visible"}},
		{"tree.set_width", "Set tree width in terminal columns.", `{"columns":{"type":"integer","minimum":8}}`, []string{"columns"}},
		{"tree.reveal", "Reveal exact workspace paths in this shell.", `{"paths":{"type":"array","items":{"type":"string"}}}`, []string{"paths"}},
		{"tree.filter", "Set this shell's file tree query.", `{"query":{"type":"string"}}`, []string{"query"}},
		{"pane.split", "Split the focused main pane.", `{"direction":{"type":"string","enum":["right","down"]}}`, []string{"direction"}},
		{"pane.merge", "Merge this shell's main panes.", `{}`, nil},
		{"pane.focus", "Focus an exact inspected group.", `{"group_id":{"type":"integer"}}`, []string{"group_id"}},
		{"pane.resize", "Resize the focused containing split.", `{"delta":{"type":"integer","minimum":-1000,"maximum":1000}}`, []string{"delta"}},
		{"tab.select", "Select an exact inspected tab.", `{"tab_id":{"type":"integer"}}`, []string{"tab_id"}},
		{"tab.close", "Close/minimize a tab; dirty documents retain their decision flow.", `{"tab_id":{"type":"integer"}}`, []string{"tab_id"}},
		{"file.open", "Open a file in this shell; positions are zero-based.", `{"path":{"type":"string"},"line":{"type":"integer","minimum":0},"column":{"type":"integer","minimum":0}}`, []string{"path"}},
		{"editor.navigate", "Navigate the focused editor, zero-based.", `{"line":{"type":"integer","minimum":0},"column":{"type":"integer","minimum":0}}`, []string{"line", "column"}},
		{"editor.select", "Select a zero-based editor range.", `{"line":{"type":"integer","minimum":0},"column":{"type":"integer","minimum":0},"end_line":{"type":"integer","minimum":0},"end_column":{"type":"integer","minimum":0}}`, []string{"line", "column", "end_line", "end_column"}},
		{"terminal.open", "Create and mount a terminal in this shell. resource_ids[0] is its terminal id for terminal.show, terminal.input and terminal.close.", `{}`, nil},
		{"terminal.show", "Show an exact retained terminal.", `{"id":{"type":"string"}}`, []string{"id"}},
		{"terminal.hide", "Hide the terminal dock without closing its PTY.", `{}`, nil},
		{"terminal.resize_dock", "Set dock rows within current shell geometry.", `{"rows":{"type":"integer","minimum":1}}`, []string{"rows"}},
		{"panel.show", "Show an existing workspace panel.", `{"id":{"type":"string"}}`, []string{"id"}},
		{"panel.hide", "Hide the workspace panel.", `{}`, nil},
		{"settings.open", "Open native settings in this shell.", `{"scope":{"type":"string","enum":["application","workspace","session"]}}`, []string{"scope"}},
		{"shell.describe_control", "Resolve exact control action effects before authorization.", `{"component_id":{"type":"string"},"operation":{"type":"object","properties":{"verb":{"type":"string"},"value":{},"item_id":{"type":"string"},"offset":{"type":"integer"},"secondary_offset":{"type":"integer"},"start":{"type":"integer"},"end":{"type":"integer"},"fraction":{"type":"number"}},"required":["verb"],"additionalProperties":false}}`, []string{"component_id", "operation"}},
	} {
		effect := Write
		if strings.HasPrefix(s.id, "shell.") {
			effect = Read
		}
		if s.id == "terminal.open" {
			effect = Execute
		}
		add(s.id, Shell, effect, s.desc, s.props, s.required...)
	}
	// This discovery-only descriptor is deliberately unresolved. It must be
	// replaced by shell.describe_control's exact descriptor before validation.
	add("shell.control", Shell, Effect(""), "Unresolved action: resolve its exact owner effect before admission.", `{"component_id":{"type":"string"},"operation":{"type":"object","properties":{"verb":{"type":"string"},"value":{},"item_id":{"type":"string"},"offset":{"type":"integer","minimum":0},"secondary_offset":{"type":"integer","minimum":0},"start":{"type":"integer","minimum":0},"end":{"type":"integer","minimum":0},"fraction":{"type":"number","minimum":0,"maximum":1}},"required":["verb"],"additionalProperties":false},"expected_action":{"type":"string"}}`, "component_id", "operation", "expected_action")
	out = append(out, ProductCommands()...)
	return out
}()
