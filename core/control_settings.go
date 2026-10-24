package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"crude/coreprotocol"
	"crude/types"
	"crude/types/control"
)

type controlSettingsInput struct {
	Scope string          `json:"scope"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

// ControlSettingsAuthority is shared with gateway descriptor resolution. This
// conservative classification cannot be bypassed with case aliases, container
// writes, or a nested authority/secret field under an otherwise ordinary page.
func ControlSettingsAuthority(path string) bool {
	return control.SettingsAuthority(path)
}

func (a *actor) prepareControlSettings(in control.Request, owner coreprotocol.Owner) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	var input controlSettingsInput
	if err := controlDecode(in.Input, &input); err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	request := coreprotocol.Request{ID: in.ID, Owner: owner, WorkspaceID: in.Target.WorkspaceID}
	prepare := func(method string, payload any) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
		request.Method = method
		request.Payload, _ = json.Marshal(payload)
		request.JSON = true
		run, apply, handled := a.prepareSettings(request)
		if !handled {
			run, apply, handled = a.prepareWorkspacePreferences(request)
		}
		if !handled {
			return nil, nil, false
		}
		var publish func(coreprotocol.Response) error
		if apply != nil {
			publish = func(result coreprotocol.Response) error { apply(result); return nil }
		}
		return wrapControlOperation(in, run, publish)
	}
	if input.Scope == "" && in.CommandID == "settings.get" {
		input.Scope = "workspace"
	}
	if input.Scope != "application" && input.Scope != "workspace" && input.Scope != "session" {
		return controlFailure(in, control.Invalid, "scope must be application, workspace or session")
	}
	if in.CommandID == "settings.get" {
		if input.Path != "" || len(input.Value) != 0 {
			return controlFailure(in, control.Invalid, "settings.get accepts scope only")
		}
		if input.Scope == "session" {
			if in.Target.SessionID == "" {
				return controlFailure(in, control.Invalid, "session target is required")
			}
			return prepare(coreprotocol.SessionSettingsRead, coreprotocol.SessionSettingsRequest{SessionID: in.Target.SessionID})
		}
		return prepare(coreprotocol.SettingsFetch, struct{}{})
	}
	if input.Path == "" {
		return controlFailure(in, control.Invalid, "exact field path is required")
	}
	if ControlSettingsAuthority(input.Path) {
		return controlFailure(in, control.Denied, "authority, secret and entity settings require a human-owned operation")
	}
	reset := in.CommandID == "settings.reset"
	if !reset && len(input.Value) == 0 {
		return controlFailure(in, control.Invalid, "value is required")
	}
	if reset && len(input.Value) != 0 {
		return controlFailure(in, control.Invalid, "reset does not accept a value")
	}
	if input.Scope == "session" {
		if in.Target.SessionID == "" {
			return controlFailure(in, control.Invalid, "exact session target is required")
		}
		backend := a.ws.BackendSession(in.Target.SessionID)
		if backend == nil {
			return controlFailure(in, control.Unavailable, "exact session is not open")
		}
		snap := backend.SettingsSnapshots()
		// Session settings use the actual DTO JSON names. Only model/account/
		// thinking/log fields belong to a session; IDE values are live inherited.
		if !controlSessionOwned(input.Path) {
			return controlFailure(in, control.Invalid, "field is not session-owned; use application/workspace scope")
		}
		// Private exact state, not the redacted read projection: replaying that
		// projection would clear write-only invocation credentials on save.
		value := coreprotocol.SessionSettingsDTO{Value: snap.Effective, MainAgent: snap.Effective.SessionMainAgent, Overrides: snap.Effective.SessionOverrides, MultiAgent: snap.Effective.SessionMultiAgent, CodeIntelligence: snap.Effective.SessionCodeIntelligence, MaxSubAgents: snap.Effective.SessionMaxSubAgents}
		value.TurnLogs = snap.Effective.SessionTurnLogs
		// The session persists exact role pins, not the effective display fields.
		value.Overrides = make(map[string]types.CategorySettings, len(snap.Overrides)+1)
		for id, cfg := range snap.Overrides {
			value.Overrides[id] = cfg
		}
		path := input.Path
		if path == "value.TurnLogsEnabled" {
			path = "turn_logs"
			if reset {
				input.Value = json.RawMessage("null")
			} else {
				var text string
				if json.Unmarshal(input.Value, &text) != nil || text != "on" && text != "off" {
					return controlFailure(in, control.Invalid, "turn logs must be on or off")
				}
				input.Value, _ = json.Marshal(text == "on")
			}
		}
		role := "default"
		if value.MainAgent != "" {
			role = "root/" + value.MainAgent
		}
		if strings.HasPrefix(path, "value.Agents.Categories.") {
			parts := strings.Split(path, ".")
			role = parts[3]
			path = "overrides." + role + "." + strings.Join(parts[4:], ".")
		} else if path == "value.Model" || path == "value.account_id" || path == "value.ThinkingEffort" {
			path = "overrides." + role + "." + strings.TrimPrefix(path, "value.")
		}
		if strings.HasPrefix(path, "overrides.") {
			parts := strings.Split(path, ".")
			if len(parts) != 3 {
				return controlFailure(in, control.Invalid, "exact role field required")
			}
			role = parts[1]
			_, known := snap.Base.Agents.Categories[role]
			if !known && role != "root/"+value.MainAgent {
				return controlFailure(in, control.Invalid, "unknown session role")
			}
			if role == "root/" {
				return controlFailure(in, control.Invalid, "selected root agent required")
			}
			if _, ok := value.Overrides[role]; !ok {
				value.Overrides[role] = types.CategorySettings{}
			}
			if reset {
				// Empty sparse pins inherit from the exact role/profile at the
				// next request boundary; copying default values pins the wrong root.
				raw, err := controlFieldJSON(types.CategorySettings{}, parts[2])
				if err != nil {
					return controlFailure(in, control.Invalid, err.Error())
				}
				input.Value = raw
			}
		} else if reset {
			raw, err := controlFieldJSON(publicSession(snap.Base), path)
			if err != nil {
				return controlFailure(in, control.Invalid, err.Error())
			}
			input.Value = raw
		}
		if err := controlSetField(&value, path, input.Value); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
		if err := controlValidateField(value, path); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
		return prepare(coreprotocol.SessionSettingsSave, coreprotocol.SessionSettingsMutationRequest{SessionID: in.Target.SessionID, Edits: []types.SettingEdit{{Path: strings.Split(path, "."), After: input.Value}}})
	}
	app := a.ws.AppDefaults()
	overrides := a.ws.LoadWorkspaceOverrides()
	before := app
	if input.Scope == "workspace" {
		before = types.MergeAppWorkspace(app, overrides)
	}
	// Resolve exact JSON metadata, including absent omitempty leaves, before
	// touching the sparse overlay. No case-insensitive encoding/json aliases.
	field, err := controlField(reflect.ValueOf(&before).Elem(), strings.Split(input.Path, "."))
	if err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	if field.Kind() == reflect.Struct || (field.Kind() == reflect.Map && field.Type().Elem().Kind() == reflect.Struct) {
		return controlFailure(in, control.Invalid, "path must name an individual field, not a settings section")
	}
	if reset {
		base := app
		if input.Scope == "application" {
			base = types.AppSettings{}
			types.ApplyAppDefaults(&base)
		}
		raw, err := controlFieldJSON(base, input.Path)
		if err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
		input.Value = raw
	}
	// Detach maps/pointers: preparing a change must never mutate the live source.
	var after types.AppSettings
	encoded, _ := json.Marshal(before)
	_ = json.Unmarshal(encoded, &after)
	if err := controlSetField(&after, input.Path, input.Value); err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	if err := controlValidatePreferences(after, input.Path); err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	if err := controlValidateField(after, input.Path); err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	if strings.HasSuffix(input.Path, ".account_id") {
		id, _ := controlFieldJSON(after, input.Path)
		var account string
		_ = json.Unmarshal(id, &account)
		if account != "" {
			if _, ok := a.ws.AllAccounts()[account]; !ok {
				return controlFailure(in, control.Invalid, "selected stored account is unavailable")
			}
		}
	}
	if input.Path == "terminal.shell" {
		if _, _, err := types.ResolveTerminalLaunch(after.Terminal, a.states.shells); err != nil {
			return controlFailure(in, control.Invalid, err.Error())
		}
	}
	if input.Scope == "application" {
		// Entity containers remain server-owned and secrets are explicit Keep.
		return prepare(coreprotocol.ApplicationPreferencesSave, coreprotocol.ApplicationPreferencesRequest{Edits: []types.SettingEdit{{Path: strings.Split(input.Path, "."), After: input.Value}}, ProxyURL: coreprotocol.SecretString{Keep: true}})
	}
	// Patch only the selected sparse leaf, preserving explicit false and all
	// neighboring overrides. Reset removes exactly this leaf, not its section.
	edit := types.SettingEdit{Path: strings.Split(input.Path, "."), After: input.Value}
	if reset {
		edit.After = nil
	}
	return prepare(coreprotocol.WorkspaceOverridesSet, coreprotocol.WorkspacePreferencesRequest{Edits: []types.SettingEdit{edit}})
}

func controlDecode(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

// Reflection is restricted to struct JSON metadata and typed values, never
// reflected methods or arbitrary workspace calls.
func controlField(v reflect.Value, parts []string) (reflect.Value, error) {
	if len(parts) == 0 {
		return v, nil
	}
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v = reflect.New(v.Type().Elem()).Elem()
		} else {
			v = v.Elem()
		}
	}
	if v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String {
		entry := v.MapIndex(reflect.ValueOf(parts[0]).Convert(v.Type().Key()))
		if !entry.IsValid() {
			return reflect.Value{}, fmt.Errorf("unknown map entry %q", parts[0])
		}
		return controlField(entry, parts[1:])
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, errors.New("path must name an exact struct JSON field")
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" || f.PkgPath != "" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if name == parts[0] {
			return controlField(v.Field(i), parts[1:])
		}
	}
	return reflect.Value{}, fmt.Errorf("unknown settings field %q", parts[0])
}

func controlFieldJSON(value any, path string) (json.RawMessage, error) {
	v, err := controlField(reflect.ValueOf(value), strings.Split(path, "."))
	if err != nil {
		return nil, err
	}
	return json.Marshal(v.Interface())
}

func controlSetField(value any, path string, raw json.RawMessage) error {
	return controlAssignField(reflect.ValueOf(value).Elem(), strings.Split(path, "."), raw)
}

func controlAssignField(root reflect.Value, parts []string, raw json.RawMessage) error {
	if root.Kind() == reflect.Pointer && len(parts) > 0 {
		if root.IsNil() {
			root.Set(reflect.New(root.Type().Elem()))
		}
		return controlAssignField(root.Elem(), parts, raw)
	}
	if root.Kind() == reflect.Map && len(parts) > 0 {
		key := reflect.ValueOf(parts[0]).Convert(root.Type().Key())
		old := root.MapIndex(key)
		if !old.IsValid() {
			return fmt.Errorf("unknown map entry %q", parts[0])
		}
		next := reflect.New(old.Type()).Elem()
		next.Set(old)
		if err := controlAssignField(next, parts[1:], raw); err != nil {
			return err
		}
		root.SetMapIndex(key, next)
		return nil
	}
	if len(parts) > 0 {
		v, err := controlField(root, parts[:1])
		if err != nil {
			return err
		}
		return controlAssignField(v, parts[1:], raw)
	}
	v := root
	var err error
	next := reflect.New(v.Type())
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && v.Kind() != reflect.Pointer && v.Kind() != reflect.Map && v.Kind() != reflect.Slice {
		return errors.New("null is not valid for this field")
	}
	if err = controlDecode(raw, next.Interface()); err != nil {
		return err
	}
	var object any
	if err = json.Unmarshal(raw, &object); err != nil {
		return err
	}
	if err = controlExactJSONKeys(v.Type(), object); err != nil {
		return err
	}
	if !v.CanSet() {
		return errors.New("field cannot be assigned")
	}
	v.Set(next.Elem())
	return nil
}

func controlExactJSONKeys(t reflect.Type, value any) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if object, ok := value.(map[string]any); ok {
		for key, child := range object {
			if t.Kind() == reflect.Map {
				if err := controlExactJSONKeys(t.Elem(), child); err != nil {
					return err
				}
				continue
			}
			if t.Kind() != reflect.Struct {
				return nil
			}
			matched := false
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name == "" {
					name = f.Name
				}
				if name == key && name != "-" && f.PkgPath == "" {
					matched = true
					if err := controlExactJSONKeys(f.Type, child); err != nil {
						return err
					}
					break
				}
			}
			if !matched {
				return fmt.Errorf("unknown exact JSON field %q", key)
			}
		}
	}
	if array, ok := value.([]any); ok && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
		for _, child := range array {
			if err := controlExactJSONKeys(t.Elem(), child); err != nil {
				return err
			}
		}
	}
	return nil
}

func controlValidatePreferences(value types.AppSettings, path string) error {
	if path == "editor" || strings.HasPrefix(path, "editor.") {
		if value.Editor.TabSize < 0 || value.Editor.TabSize > 16 {
			return errors.New("tab_size must be 0..16")
		}
		if value.Editor.AutoSaveDelay != 0 && (value.Editor.AutoSaveDelay < 250 || value.Editor.AutoSaveDelay > 60000) {
			return errors.New("auto_save_delay must be 250..60000 ms")
		}
		switch value.Editor.AutoSave {
		case "", types.AutoSaveOff, types.AutoSaveAfterDelay, types.AutoSaveEditorFocusChange, types.AutoSaveTerminalFocusLoss:
		default:
			return errors.New("unknown auto_save mode")
		}
	}
	if path == "lsp" || strings.HasPrefix(path, "lsp.") {
		return value.LSP.Validate()
	}
	if path == "terminal.scrollback_lines" && value.Terminal.ScrollbackLines != 0 && (value.Terminal.ScrollbackLines < 100 || value.Terminal.ScrollbackLines > 10000) {
		return errors.New("scrollback_lines must be 100..10000")
	}
	return nil
}
