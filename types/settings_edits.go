package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// SettingEdit sets one explicit value or removes it when After is nil.
// Paths are segments so user-defined identifiers may contain dots.
type SettingEdit struct {
	Path  []string        `json:"path"`
	After json.RawMessage `json:"after,omitempty"`
}

// SettingsEdits compares drafts of the same layer, never a child with its
// parent. Unchanged inherited values therefore cannot become overrides.
func SettingsEdits(before, after any) []SettingEdit {
	var edits []SettingEdit
	diffSettingsTree(nil, settingsTree(reflect.ValueOf(before)), settingsTree(reflect.ValueOf(after)), &edits)
	return edits
}

func diffSettingsTree(path []string, before, after any, edits *[]SettingEdit) {
	a, aObject := before.(map[string]any)
	b, bObject := after.(map[string]any)
	if aObject && bObject {
		keys := make([]string, 0, len(a)+len(b))
		for key := range a {
			keys = append(keys, key)
		}
		for key := range b {
			if _, ok := a[key]; !ok {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		for _, key := range keys {
			av, aok := a[key]
			bv, bok := b[key]
			p := append(slices.Clone(path), key)
			if !aok || !bok {
				if bok {
					if _, object := bv.(map[string]any); object {
						diffSettingsTree(p, map[string]any{}, bv, edits)
						continue
					}
				}
				edit := SettingEdit{Path: p}
				if bok {
					edit.After, _ = json.Marshal(bv)
				}
				*edits = append(*edits, edit)
			} else {
				diffSettingsTree(p, av, bv, edits)
			}
		}
		return
	}
	aJSON, _ := json.Marshal(before)
	bJSON, _ := json.Marshal(after)
	if !bytes.Equal(aJSON, bJSON) {
		*edits = append(*edits, SettingEdit{Path: slices.Clone(path), After: bJSON})
	}
}

// ApplySettingsEdits applies only the addressed fields to the latest layer.
// The last explicit write wins; untouched neighbors are never replayed.
func ApplySettingsEdits(dst any, edits []SettingEdit) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("settings destination must be a pointer")
	}
	tree := settingsTree(v.Elem())
	if err := editSettingsTree(tree, edits); err != nil {
		return err
	}
	data, err := json.Marshal(tree)
	if err != nil {
		return err
	}
	next := reflect.New(v.Elem().Type())
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(next.Interface()); err != nil {
		return err
	}
	v.Elem().Set(next.Elem())
	return nil
}

// PatchStoredSettings preserves unknown/stale neighbors in a stored document.
// Callers validate new values using their current typed schema before writing.
func PatchStoredSettings(data json.RawMessage, edits []SettingEdit) (json.RawMessage, error) {
	var tree map[string]any
	if len(data) != 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &tree); err != nil {
			return nil, err
		}
	}
	if tree == nil {
		tree = make(map[string]any)
	}
	if err := editSettingsTree(tree, edits); err != nil {
		return nil, err
	}
	return json.Marshal(tree)
}

func editSettingsTree(tree any, edits []SettingEdit) error {
	for _, edit := range edits {
		if len(edit.Path) == 0 {
			return fmt.Errorf("an exact settings field is required")
		}
		object, ok := tree.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid settings object")
		}
		parents := []map[string]any{object}
		for _, key := range edit.Path[:len(edit.Path)-1] {
			next, ok := object[key].(map[string]any)
			if !ok {
				next = make(map[string]any)
				object[key] = next
			}
			object = next
			parents = append(parents, object)
		}
		key := edit.Path[len(edit.Path)-1]
		if edit.After == nil {
			delete(object, key)
			for i := len(parents) - 1; i > 0 && len(parents[i]) == 0; i-- {
				delete(parents[i-1], edit.Path[i-1])
			}
		} else {
			var value any
			if err := json.Unmarshal(edit.After, &value); err != nil {
				return err
			}
			object[key] = value
		}
	}
	return nil
}

// settingsTree includes zero values omitted by wire JSON. Presence is meaningful
// in a stored overlay; Go's omitempty is not a user's inheritance choice.
func settingsTree(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return settingsTree(v.Elem())
	}
	if v.Type() == reflect.TypeFor[json.RawMessage]() {
		var tree any
		_ = json.Unmarshal(v.Bytes(), &tree)
		return tree
	}
	switch v.Kind() {
	case reflect.Struct:
		out := make(map[string]any)
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if f.PkgPath != "" || name == "-" {
				continue
			}
			if v.Field(i).Type() == reflect.TypeFor[json.RawMessage]() && v.Field(i).IsNil() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			out[name] = settingsTree(v.Field(i))
		}
		return out
	case reflect.Map:
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = settingsTree(iter.Value())
		}
		return out
	default:
		raw, _ := json.Marshal(v.Interface())
		var out any
		_ = json.Unmarshal(raw, &out)
		return out
	}
}

// DecodeStoredSettings reads current-schema leaves independently. Unknown and
// obsolete ordinary fields inherit; one wrong type cannot poison a whole layer.
// It does not translate old schemas or rewrite the original document.
func DecodeStoredSettings(data []byte, dst any) error {
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		return err
	}
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("settings destination must be a pointer")
	}
	decodeStoredValue(tree, v.Elem())
	return nil
}

func decodeStoredValue(tree any, dst reflect.Value) {
	if tree == nil {
		return
	}
	if dst.Kind() == reflect.Pointer {
		next := reflect.New(dst.Type().Elem())
		if dst.Type().Elem().Kind() != reflect.Struct {
			raw, _ := json.Marshal(tree)
			if json.Unmarshal(raw, next.Interface()) != nil {
				return
			}
		} else if _, ok := tree.(map[string]any); !ok {
			return
		}
		if !dst.IsNil() {
			next.Elem().Set(dst.Elem())
		}
		decodeStoredValue(tree, next.Elem())
		dst.Set(next)
		return
	}
	object, isObject := tree.(map[string]any)
	if dst.Type() != reflect.TypeFor[json.RawMessage]() && isObject {
		switch dst.Kind() {
		case reflect.Struct:
			for i := 0; i < dst.NumField(); i++ {
				field := dst.Type().Field(i)
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if field.PkgPath != "" || name == "-" {
					continue
				}
				if name == "" {
					name = field.Name
				}
				if value, exists := object[name]; exists {
					decodeStoredValue(value, dst.Field(i))
				}
			}
			return
		case reflect.Map:
			if dst.Type().Key().Kind() != reflect.String {
				return
			}
			if dst.IsNil() {
				dst.Set(reflect.MakeMap(dst.Type()))
			}
			for key, value := range object {
				entry := reflect.New(dst.Type().Elem()).Elem()
				if old := dst.MapIndex(reflect.ValueOf(key)); old.IsValid() {
					entry.Set(old)
				}
				decodeStoredValue(value, entry)
				dst.SetMapIndex(reflect.ValueOf(key), entry)
			}
			return
		}
	}
	raw, _ := json.Marshal(tree)
	next := reflect.New(dst.Type())
	if json.Unmarshal(raw, next.Interface()) == nil {
		dst.Set(next.Elem())
	}
}

// ValidateSettingEdits checks new input against exact current JSON metadata.
// Stored reads are tolerant; new writes must not silently accept misspellings.
func ValidateSettingEdits(schema any, edits []SettingEdit) error {
	for _, edit := range edits {
		t := reflect.TypeOf(schema)
		for _, part := range edit.Path {
			for t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			if t.Kind() == reflect.Map && t.Key().Kind() == reflect.String {
				t = t.Elem()
				continue
			}
			if t.Kind() != reflect.Struct {
				return fmt.Errorf("unknown settings field %s", strings.Join(edit.Path, "."))
			}
			found := false
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name == "" {
					name = f.Name
				}
				if f.PkgPath == "" && name != "-" && name == part {
					t = f.Type
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("unknown settings field %s", strings.Join(edit.Path, "."))
			}
		}
		if len(edit.Path) == 0 {
			return fmt.Errorf("exact settings field required")
		}
		if edit.After != nil {
			value := reflect.New(t)
			decoder := json.NewDecoder(bytes.NewReader(edit.After))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(value.Interface()); err != nil {
				return fmt.Errorf("%s: %w", strings.Join(edit.Path, "."), err)
			}
		}
	}
	return nil
}
