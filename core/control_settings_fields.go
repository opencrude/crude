package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"crude/types"
	"crude/types/control"
)

type controlSettingsFieldsInput struct {
	Scope  string `json:"scope"`
	Prefix string `json:"prefix,omitempty"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}
type controlSettingsField struct {
	Path      string          `json:"path"`
	Value     json.RawMessage `json:"value,omitempty"`
	Type      string          `json:"type"`
	Schema    map[string]any  `json:"schema"`
	Ownership string          `json:"ownership"`
	Inherited bool            `json:"inherited"`
	ReadOnly  bool            `json:"readonly"`
	HumanOnly bool            `json:"humanOnly"`
}
type controlSettingsFieldsPage struct {
	Fields          []controlSettingsField `json:"fields"`
	Revision        string                 `json:"revision"`
	SessionRevision *uint64                `json:"session_revision,omitempty"`
	Cursor          string                 `json:"cursor,omitempty"`
}

func controlSessionOwned(path string) bool {
	switch path {
	case "value.Model", "value.account_id", "value.ThinkingEffort", "value.TurnLogsEnabled", "main_agent", "multi_agent":
		return true
	}
	if strings.HasPrefix(path, "overrides.") || strings.HasPrefix(path, "value.Agents.Categories.") {
		switch path[strings.LastIndex(path, ".")+1:] {
		case "Model", "account_id", "ThinkingEffort", "context_compaction_percent":
			return true
		}
	}
	return false
}

func controlFieldSchema(t reflect.Type, path string) map[string]any {
	nullable := t.Kind() == reflect.Pointer
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	kind := "string"
	switch t.Kind() {
	case reflect.Bool:
		kind = "boolean"
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64:
		kind = "integer"
	case reflect.Float32, reflect.Float64:
		kind = "number"
	case reflect.Map, reflect.Struct:
		kind = "object"
	case reflect.Slice, reflect.Array:
		kind = "array"
	}
	schema := control.StructSchema(t)
	if nullable {
		schema["type"] = []string{kind, "null"}
	}
	if kind == "array" {
		schema["items"] = controlFieldSchema(t.Elem(), path+"[]")
	}
	if leaf := path[strings.LastIndex(path, ".")+1:]; leaf == "ModelModalities" || leaf == "model_modalities" {
		schema["items"] = map[string]any{"type": "string", "enum": []string{"image", "pdf", "audio", "video"}}
	}
	leaf := path[strings.LastIndex(path, ".")+1:]
	enums := map[string][]string{
		"preset":         {"dark", "light", "high-contrast", "warm", "midnight"},
		"min_severity":   {"", "error", "warning", "info", "hint"},
		"ThinkingEffort": {"", "none", "low", "medium", "high", "xhigh", "max"}, "thinking_effort": {"", "none", "low", "medium", "high", "xhigh", "max"},
		"auto_save": {"off", "after_delay", "editor_focus_change", "terminal_focus_loss"}, "syntax_theme": {"catppuccin", "dracula", "github", "monokai", "solarized"}, "render_whitespace": {"none", "boundary", "all"}, "context_help": {"compact", "expanded", "off"}, "placement": {"docked", "floating"}, "login_shell": {"on", "off"}, "TurnLogsEnabled": {"", "on", "off"},
	}
	if e, ok := enums[leaf]; ok {
		schema["enum"] = e
	}
	if strings.HasPrefix(path, "memory.") || strings.HasPrefix(path, "local_history.") {
		switch leaf {
		case "enabled", "auto_recall", "auto_extract", "consolidation", "use_gitignore", "capture_external_changes", "safety_snapshot_before_restore":
			schema["enum"] = []string{"", "on", "off"}
		}
	}
	bounds := map[string][2]float64{"tab_size": {0, 16}, "auto_save_delay": {250, 60000}, "scrollback_lines": {100, 10000}, "context_compaction_percent": {0, 100}, "ModelTemperature": {0, 2}, "model_temperature": {0, 2}, "ModelTopP": {0, 1}, "model_top_p": {0, 1}, "pad_top": {0, 100}, "pad_bottom": {0, 100}, "pad_left": {0, 100}, "pad_right": {0, 100}}
	if b, ok := bounds[leaf]; ok {
		schema["minimum"], schema["maximum"] = b[0], b[1]
	} else if kind == "integer" || kind == "number" {
		schema["minimum"] = 0
	}
	if kind == "integer" && schema["maximum"] == nil {
		schema["maximum"] = float64(2147483647)
	}
	if path == "font.size" {
		schema["minimum"], schema["maximum"] = float64(1), float64(200)
	}
	return schema
}

func controlValidateField(value any, path string) error {
	v, err := controlField(reflect.ValueOf(value), strings.Split(path, "."))
	if err != nil {
		return err
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	s := controlFieldSchema(v.Type(), path)
	if v.Kind() == reflect.Slice {
		if items, ok := s["items"].(map[string]any); ok {
			if enums, ok := items["enum"].([]string); ok {
				for i := 0; i < v.Len(); i++ {
					valid := false
					for _, e := range enums {
						valid = valid || e == v.Index(i).String()
					}
					if !valid {
						return fmt.Errorf("%s contains an unknown choice", path)
					}
				}
			}
		}
	}
	if e, ok := s["enum"].([]string); ok {
		found := false
		for _, item := range e {
			found = found || v.String() == item
		}
		if !found {
			return fmt.Errorf("%s is not an allowed choice", path)
		}
	}
	var n float64
	switch v.Kind() {
	case reflect.Int, reflect.Int64:
		n = float64(v.Int())
	case reflect.Float64:
		n = v.Float()
	default:
		return nil
	}
	if min, ok := s["minimum"].(float64); ok && n < min {
		return fmt.Errorf("%s is below minimum", path)
	}
	if min, ok := s["minimum"].(int); ok && n < float64(min) {
		return fmt.Errorf("%s is below minimum", path)
	}
	if max, ok := s["maximum"].(float64); ok && n > max {
		return fmt.Errorf("%s is above maximum", path)
	}
	return nil
}

func controlSettingsWalk(v reflect.Value, path string, visit func(string, reflect.Value)) {
	if v.Kind() == reflect.Pointer && v.Type().Elem().Kind() != reflect.Struct {
		visit(path, v)
		return
	}
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v = reflect.New(v.Type().Elem()).Elem()
		} else {
			v = v.Elem()
		}
	}
	if v.Kind() == reflect.Struct {
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" || f.PkgPath != "" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			p := name
			if path != "" {
				p = path + "." + name
			}
			controlSettingsWalk(v.Field(i), p, visit)
		}
	} else if v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String && v.Type().Elem().Kind() == reflect.Struct {
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, key := range keys {
			if strings.Contains(key.String(), ".") {
				continue
			}
			controlSettingsWalk(v.MapIndex(key), path+"."+key.String(), visit)
		}
	} else {
		visit(path, v)
	}
}

func (a *actor) prepareControlSettingsFields(in control.Request) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	var input controlSettingsFieldsInput
	if err := controlDecode(in.Input, &input); err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	if input.Limit < 1 || input.Limit > control.MaxPageLimit {
		return controlFailure(in, control.Invalid, "limit must be 1..128")
	}
	var value any
	var overlay map[string]any
	page := controlSettingsFieldsPage{Fields: []controlSettingsField{}}
	switch input.Scope {
	case "application", "workspace":
		app := a.ws.AppDefaults()
		overrides := a.ws.LoadWorkspaceOverrides()
		page.Revision = entityRevision(app)
		value = app
		if input.Scope == "workspace" {
			page.Revision = entityRevision(overrides)
			value = types.MergeAppWorkspace(app, overrides)
			raw, _ := json.Marshal(overrides)
			_ = json.Unmarshal(raw, &overlay)
		}
	case "session":
		backend := a.ws.BackendSession(in.Target.SessionID)
		if backend == nil {
			return controlFailure(in, control.Unavailable, "exact open session target required")
		}
		snap := backend.SettingsSnapshots()
		value = publicSession(snap.Effective)
		page.SessionRevision = &snap.Revision
		page.Revision = strconv.FormatUint(snap.Revision, 10)
	default:
		return controlFailure(in, control.Invalid, "scope must be application, workspace or session")
	}
	start := ""
	if input.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		parts := strings.SplitN(string(raw), "\n", 3)
		if err != nil || len(parts) != 3 || parts[0] != page.Revision || parts[1] != input.Scope+":"+input.Prefix {
			return controlFailure(in, control.Stale, "field cursor no longer matches settings")
		}
		start = parts[2]
	}
	var fields []controlSettingsField
	controlSettingsWalk(reflect.ValueOf(value), "", func(path string, v reflect.Value) {
		if !strings.HasPrefix(path, input.Prefix) || path <= start {
			return
		}
		// Catalog/cache fields are not ordinary form controls.
		if strings.Contains(path, ".Profiles") || strings.Contains(path, "Price") || strings.HasSuffix(path, "ActiveProfile") {
			return
		}
		human := ControlSettingsAuthority(path)
		readonly := false
		ownership := input.Scope
		if input.Scope == "workspace" && (strings.HasPrefix(path, "accounts.") || strings.HasPrefix(path, "network.")) {
			readonly = true
			ownership = "application"
		}
		if input.Scope == "session" && !controlSessionOwned(path) {
			readonly = true
			ownership = "workspace"
		}
		inherited := false
		if input.Scope == "workspace" {
			var node any = overlay
			for _, part := range strings.Split(path, ".") {
				m, _ := node.(map[string]any)
				node = m[part]
			}
			inherited = node == nil
		}
		field := controlSettingsField{Path: path, Type: v.Type().String(), Schema: controlFieldSchema(v.Type(), path), Ownership: ownership, Inherited: inherited, ReadOnly: readonly, HumanOnly: human}
		// Human-owned containers may contain unlabelled credentials. Never export
		// their values; metadata remains discoverable without secret inference.
		if !human {
			raw, _ := json.Marshal(v.Interface())
			if len(raw) <= control.MaxValueBytes {
				field.Value = raw
			} else {
				field.ReadOnly = true
			}
		}
		fields = append(fields, field)
	})
	sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
	for i, field := range fields {
		candidate := page
		candidate.Fields = append(append([]controlSettingsField(nil), page.Fields...), field)
		raw, _ := json.Marshal(candidate)
		if i >= input.Limit || len(raw) > control.MaxValueBytes-1024 {
			if len(page.Fields) == 0 {
				return controlFailure(in, control.Unavailable, "field descriptor exceeds discovery budget")
			}
			page.Cursor = base64.RawURLEncoding.EncodeToString([]byte(page.Revision + "\n" + input.Scope + ":" + input.Prefix + "\n" + page.Fields[len(page.Fields)-1].Path))
			break
		}
		page.Fields = append(page.Fields, field)
	}
	raw, _ := json.Marshal(page)
	return func(context.Context) control.Result {
		return control.Result{RequestID: in.ID, State: control.Committed, Output: raw}
	}, nil, true
}
