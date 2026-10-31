package lspnav

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	locationJSON = `{"uri":"file:///b.go","range":{"start":{"line":2,"character":3},"end":{"line":4,"character":1}}}`
	linkJSON     = `{"targetUri":"custom:target","targetRange":{"start":{"line":2,"character":3},"end":{"line":4,"character":1}},"targetSelectionRange":{"start":{"line":3,"character":8},"end":{"line":3,"character":9}}}`
)

func TestParseStandardUnions(t *testing.T) {
	location := Target{
		URI:       "file:///b.go",
		Range:     Range{Start: Position{2, 3}, End: Position{4, 1}},
		Selection: Range{Start: Position{2, 3}, End: Position{4, 1}},
	}
	link := Target{
		URI:       "custom:target",
		Range:     Range{Start: Position{2, 3}, End: Position{4, 1}},
		Selection: Range{Start: Position{3, 8}, End: Position{3, 9}},
		Link:      true,
	}
	kinds := []Kind{Definition, TypeDefinition, Implementation, References}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			tests := []struct {
				name, raw string
				want      Result
			}{
				{"null", " \nnull\t", Result{Answer: NullAnswer}},
				{"empty", " [ \r\n ] ", Result{Answer: EmptyAnswer}},
				{"locations", "[" + locationJSON + "]", Result{Answer: TargetsAnswer, Targets: []Target{location}}},
				// Duplicate targets are not an ambiguous JSON shape and stay ordered.
				{"repeated", "[" + locationJSON + "," + locationJSON + "]", Result{Answer: TargetsAnswer, Targets: []Target{location, location}}},
			}
			if kind != References {
				tests = append(tests,
					struct {
						name, raw string
						want      Result
					}{"single", locationJSON, Result{Answer: TargetsAnswer, Targets: []Target{location}}},
					struct {
						name, raw string
						want      Result
					}{"links", "[" + linkJSON + "]", Result{Answer: TargetsAnswer, Targets: []Target{link}}},
				)
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					got, err := Parse(kind, []byte(tt.raw))
					if err != nil || !reflect.DeepEqual(got, tt.want) {
						t.Fatalf("got %#v, %v; want %#v", got, err, tt.want)
					}
				})
			}
			requireRejected(t, kind, linkJSON)
			if kind == References {
				requireRejected(t, kind, locationJSON)
				requireRejected(t, kind, "["+linkJSON+"]")
			}
		})
	}
	// The public literals and bounds are part of the agreed contract.
	if Definition != "definition" || TypeDefinition != "type-definition" ||
		Implementation != "implementation" || References != "references" ||
		TargetsAnswer != "targets" || NullAnswer != "null" || EmptyAnswer != "empty" ||
		MaxRawBytes != 1048576 || MaxTargets != 1024 || MaxURIBytes != 4096 {
		t.Fatal("public contract changed")
	}
}

func TestParsePreservesUnicodeURIOrderAndLinkRanges(t *testing.T) {
	raw := []byte(`[
		{"targetUri":"FILE:\/\/host\/a\/..\/%2f\/雪\uD83D\uDE80",
		 "originSelectionRange":{"start":{"line":99,"character":5},"end":{"line":100,"character":0}},
		 "targetRange":{"start":{"line":1,"character":5},"end":{"line":3,"character":2}},
		 "targetSelectionRange":{"start":{"line":1,"character":5},"end":{"line":3,"character":2}}},
		{"targetUri":"../not-authorized/é","targetRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},
		 "targetSelectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}}}
	]`)
	original := bytes.Clone(raw)
	want := Result{Answer: TargetsAnswer, Targets: []Target{
		{
			URI:       "FILE://host/a/../%2f/雪🚀",
			Range:     Range{Start: Position{1, 5}, End: Position{3, 2}},
			Selection: Range{Start: Position{1, 5}, End: Position{3, 2}},
			Origin:    &Range{Start: Position{99, 5}, End: Position{100, 0}},
			Link:      true,
		},
		{URI: "../not-authorized/é", Link: true},
	}}
	got, err := Parse(Implementation, raw)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v; want %#v", got, err, want)
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("parser mutated original JSON")
	}
	// Parsed strings and origin data are independent of the caller's raw buffer.
	for i := range raw {
		raw[i] = ' '
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("result aliases original JSON")
	}

	// Accepted URI data is opaque: no scheme, percent-decoding or text policy.
	raw = []byte(`{"uri":"opaque:\"\u005c\uFFFD\n\u0001","range":{"start":{"line":0,"character":1},"end":{"line":0,"character":1}}}`)
	got, err = Parse(Definition, raw)
	want = Result{Answer: TargetsAnswer, Targets: []Target{{
		URI: "opaque:\"\\�\n\x01", Range: Range{Position{0, 1}, Position{0, 1}},
		Selection: Range{Position{0, 1}, Position{0, 1}},
	}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("opaque URI got %#v, %v; want %#v", got, err, want)
	}
}

func TestParseRejectsMalformedAndMixedResponsesAtomically(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"absent", ""},
		{"whitespace", " \t\r\n"},
		{"object empty", "{}"},
		{"string", `"null"`},
		{"boolean", "true"},
		{"number", "0"},
		{"trailing value", locationJSON + " null"},
		{"trailing garbage", locationJSON + "!"},
		{"trailing comma", "[" + locationJSON + ",]"},
		{"unterminated array", "[" + locationJSON},
		{"null row", "[" + locationJSON + ",null]"},
		{"array row", "[" + locationJSON + ",[]]"},
		{"string row", "[" + locationJSON + `,"x"]`},
		{"late missing range", "[" + locationJSON + `,{"uri":"file:///later"}]`},
		{"mixed location then link", "[" + locationJSON + "," + linkJSON + "]"},
		{"mixed link then location", "[" + linkJSON + "," + locationJSON + "]"},
		{"both URIs", strings.Replace(locationJSON, `"uri":`, `"targetUri":"custom:x","uri":`, 1)},
		{"link fields in location", strings.Replace(locationJSON, `"uri":`, `"targetRange":null,"uri":`, 1)},
		{"origin in location", strings.Replace(locationJSON, `"uri":`, `"originSelectionRange":null,"uri":`, 1)},
		{"range in link", "[" + strings.Replace(linkJSON, `"targetUri":`, `"range":null,"targetUri":`, 1) + "]"},
		{"unknown metadata", strings.Replace(locationJSON, `"uri":`, `"metadata":{"note":"x"},"uri":`, 1)},
		{"operative extra", strings.Replace(locationJSON, `"uri":`, `"targetSelectionRange":null,"uri":`, 1)},
		{"case changed key", strings.Replace(locationJSON, `"uri"`, `"URI"`, 1)},
		{"URI missing", `{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}}}`},
		{"URI null", strings.Replace(locationJSON, `"file:///b.go"`, "null", 1)},
		{"URI number", strings.Replace(locationJSON, `"file:///b.go"`, "1", 1)},
		{"URI empty", strings.Replace(locationJSON, `"file:///b.go"`, `""`, 1)},
		{"URI NUL", strings.Replace(locationJSON, `"file:///b.go"`, `"file:///bad\u0000.go"`, 1)},
		{"range missing", `{"uri":"file:///a"}`},
		{"range null", `{"uri":"file:///a","range":null}`},
		{"range array", `{"uri":"file:///a","range":[]}`},
		{"range start missing", `{"uri":"file:///a","range":{"end":{"line":0,"character":0}}}`},
		{"range end missing", `{"uri":"file:///a","range":{"start":{"line":0,"character":0}}}`},
		{"position null", strings.Replace(locationJSON, `{"line":2,"character":3}`, "null", 1)},
		{"position scalar", strings.Replace(locationJSON, `{"line":2,"character":3}`, "3", 1)},
		{"position line missing", strings.Replace(locationJSON, `"line":2,`, "", 1)},
		{"position character missing", strings.Replace(locationJSON, `,"character":3`, "", 1)},
		{"position extra", strings.Replace(locationJSON, `"line":2`, `"line":2,"uri":"x"`, 1)},
		{"range extra", strings.Replace(locationJSON, `"start":`, `"targetRange":null,"start":`, 1)},
		{"reversed line", strings.Replace(locationJSON, `"line":4`, `"line":1`, 1)},
		{"reversed character", strings.Replace(locationJSON, `"line":4`, `"line":2`, 1)},
		{"link selection missing", `[{"targetUri":"x","targetRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}}}]`},
		{"link target range missing", `[{"targetUri":"x","targetSelectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}}}]`},
		{"link origin null", "[" + strings.Replace(linkJSON, `"targetUri":`, `"originSelectionRange":null,"targetUri":`, 1) + "]"},
		{"link reversed selection", "[" + strings.Replace(linkJSON, `"character":9`, `"character":7`, 1) + "]"},
		{"link selection before target", "[" + strings.Replace(linkJSON, `"line":3,"character":8`, `"line":2,"character":2`, 1) + "]"},
		{"link selection after target", "[" + strings.Replace(linkJSON, `"line":3,"character":9`, `"line":4,"character":2`, 1) + "]"},
		{"link reversed origin", "[" + strings.Replace(linkJSON, `"targetUri":`,
			`"originSelectionRange":{"start":{"line":8,"character":0},"end":{"line":7,"character":0}},"targetUri":`, 1) + "]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, kind := range []Kind{Definition, TypeDefinition, Implementation, References} {
				requireRejected(t, kind, tt.raw)
			}
		})
	}
	for _, kind := range []Kind{"", "Definition", "hover", "unsupported"} {
		for _, raw := range []string{"null", "[]", locationJSON} {
			requireRejected(t, kind, raw)
		}
	}
}

func TestParseRejectsNoncanonicalCoordinates(t *testing.T) {
	for _, token := range []string{
		"-1", "-0", "0.0", "2.0", "2e0", "2E+0", "2147483648",
		"9999999999999999999999999", "null", "true", `"2"`, "[]", "{}",
		"02", "+2", "NaN", "Infinity",
	} {
		t.Run(token, func(t *testing.T) {
			// Exercise every coordinate, not just the first start-line slot.
			for _, original := range []string{`"line":2`, `"character":3`, `"line":4`, `"character":1`} {
				key := strings.Split(original, ":")[0]
				raw := strings.Replace(locationJSON, original, key+":"+token, 1)
				requireRejected(t, Definition, raw)
			}
		})
	}
	raw := []byte(`{"uri":"x","range":{"start":{"line":0,"character":2147483647},"end":{"line":2147483647,"character":2147483647}}}`)
	want := Result{Answer: TargetsAnswer, Targets: []Target{{
		URI:       "x",
		Range:     Range{Position{0, 2147483647}, Position{2147483647, 2147483647}},
		Selection: Range{Position{0, 2147483647}, Position{2147483647, 2147483647}},
	}}}
	got, err := Parse(Definition, raw)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("int32 boundary got %#v, %v; want %#v", got, err, want)
	}
}

func TestParseRejectsDuplicateMembersAndInvalidUnicode(t *testing.T) {
	for name, raw := range map[string]string{
		"duplicate URI":                  strings.Replace(locationJSON, `"uri":`, `"uri":"first","uri":`, 1),
		"escaped URI":                    strings.Replace(locationJSON, `"uri":`, `"\u0075ri":"first","uri":`, 1),
		"duplicate range":                strings.Replace(locationJSON, `"range":`, `"range":null,"range":`, 1),
		"escaped range start":            strings.Replace(locationJSON, `"start":`, `"\u0073tart":null,"start":`, 1),
		"escaped coordinate":             strings.Replace(locationJSON, `"line":2`, `"\u006cine":1,"line":2`, 1),
		"late duplicate":                 "[" + locationJSON + "," + strings.Replace(locationJSON, `"uri":`, `"uri":"first","uri":`, 1) + "]",
		"escaped link key":               "[" + strings.Replace(linkJSON, `"targetUri":`, `"\u0074argetUri":"first","targetUri":`, 1) + "]",
		"high surrogate":                 strings.Replace(locationJSON, "file:///b.go", `file:///\uD800`, 1),
		"low surrogate":                  strings.Replace(locationJSON, "file:///b.go", `file:///\uDC00`, 1),
		"high before letter":             strings.Replace(locationJSON, "file:///b.go", `\uD800\u0041`, 1),
		"high before high":               strings.Replace(locationJSON, "file:///b.go", `\uD800\uDBFF`, 1),
		"reversed surrogate pair":        strings.Replace(locationJSON, "file:///b.go", `\uDC00\uD800`, 1),
		"separated surrogate pair":       strings.Replace(locationJSON, "file:///b.go", `\uD800x\uDC00`, 1),
		"surrogate in key":               strings.Replace(locationJSON, `"uri"`, `"\uD800"`, 1),
		"surrogate before escaped slash": strings.Replace(locationJSON, "file:///b.go", `\uD800\\uDC00`, 1),
		"bad escape":                     strings.Replace(locationJSON, "file:///b.go", `\uZZZZ`, 1),
		"truncated escape":               `{"uri":"\uD8`,
		"invalid UTF8 in URI":            strings.Replace(locationJSON, "file:///b.go", "file:///\xff", 1),
		"invalid UTF8 outside string":    locationJSON + "\xff",
	} {
		t.Run(name, func(t *testing.T) {
			requireRejected(t, Definition, raw)
		})
	}
	// Escaped-equivalent single keys and literal backslash-u text remain valid.
	raw := strings.Replace(locationJSON, `"uri":"file:///b.go"`, `"\u0075ri":"literal:\\uD800\\uDC00"`, 1)
	got, err := Parse(Definition, []byte(raw))
	want := Result{Answer: TargetsAnswer, Targets: []Target{{
		URI:       `literal:\uD800\uDC00`,
		Range:     Range{Position{2, 3}, Position{4, 1}},
		Selection: Range{Position{2, 3}, Position{4, 1}},
	}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("escaped data got %#v, %v; want %#v", got, err, want)
	}
}

func TestParseBudgets(t *testing.T) {
	for _, n := range []int{4095, 4096, 4097} {
		// A multibyte suffix proves the URI budget counts decoded UTF-8 bytes.
		value := strings.Repeat("a", n-3) + "雪"
		quoted, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		raw := strings.Replace(locationJSON, `"file:///b.go"`, string(quoted), 1)
		got, err := Parse(Definition, []byte(raw))
		if n > 4096 {
			if err == nil || !reflect.DeepEqual(got, Result{}) {
				t.Fatalf("URI overflow got %#v, %v", got, err)
			}
		} else if err != nil || got.Answer != TargetsAnswer || len(got.Targets) != 1 || got.Targets[0].URI != value {
			t.Fatalf("URI boundary %d got %#v, %v", n, got, err)
		}
	}
	// Wire spelling is not the URI budget: each escape decodes to one byte.
	escaped := strings.Repeat(`\u0061`, 4096)
	got, err := Parse(Definition, []byte(strings.Replace(locationJSON, "file:///b.go", escaped, 1)))
	if err != nil || len(got.Targets) != 1 || got.Targets[0].URI != strings.Repeat("a", 4096) {
		t.Fatalf("escaped URI boundary: %v", err)
	}
	for _, n := range []int{1023, 1024, 1025} {
		for _, row := range []string{locationJSON, linkJSON} {
			raw := "[" + strings.Repeat(row+",", n-1) + row + "]"
			got, err := Parse(Definition, []byte(raw))
			if n > 1024 {
				if err == nil || !reflect.DeepEqual(got, Result{}) {
					t.Fatalf("count overflow got %d targets, %v", len(got.Targets), err)
				}
			} else if err != nil || got.Answer != TargetsAnswer || len(got.Targets) != n {
				t.Fatalf("count boundary %d got %d targets, %v", n, len(got.Targets), err)
			}
		}
	}
	for _, n := range []int{1048575, 1048576, 1048577} {
		raw := append([]byte("null"), bytes.Repeat([]byte(" "), n-4)...)
		got, err := Parse(Definition, raw)
		if n > 1048576 {
			if err == nil || !reflect.DeepEqual(got, Result{}) {
				t.Fatalf("raw overflow got %#v, %v", got, err)
			}
		} else if err != nil || !reflect.DeepEqual(got, Result{Answer: NullAnswer}) {
			t.Fatalf("raw boundary %d got %#v, %v", n, got, err)
		}
	}
	// Closed navigation shapes cannot contain 64 containers. Check the JSON
	// guard's exact boundary separately, and rejection via the public entry point.
	for _, n := range []int{63, 64, 65, 1000} {
		raw := strings.Repeat("[", n) + "0" + strings.Repeat("]", n)
		err := validateJSON([]byte(raw))
		if (err == nil) != (n <= 64) {
			t.Fatalf("depth %d: %v", n, err)
		}
		requireRejected(t, Definition, raw)
	}
}

func requireRejected(t *testing.T, kind Kind, raw string) {
	t.Helper()
	got, err := Parse(kind, []byte(raw))
	if err == nil || !reflect.DeepEqual(got, Result{}) {
		t.Fatalf("kind %q: expected error and zero Result, got %#v, %v; raw %.180q", kind, got, err, raw)
	}
}

func FuzzParse(f *testing.F) {
	for _, raw := range []string{
		"null", "[]", locationJSON, "[" + locationJSON + "]", "[" + linkJSON + "]",
		"[" + locationJSON + ",null]", "[" + locationJSON + "," + linkJSON + "]",
		strings.Replace(locationJSON, `"uri":`, `"\u0075ri":"first","uri":`, 1),
		strings.Replace(locationJSON, "file:///b.go", `\uD800`, 1),
	} {
		f.Add(raw, uint8(0))
	}
	f.Fuzz(func(t *testing.T, raw string, selector uint8) {
		kinds := [...]Kind{Definition, TypeDefinition, Implementation, References, "unknown"}
		kind := kinds[int(selector)%len(kinds)]
		original := []byte(raw)
		got, err := Parse(kind, original)
		if string(original) != raw {
			t.Fatal("input changed")
		}
		if err != nil {
			if !reflect.DeepEqual(got, Result{}) {
				t.Fatal("partial error result")
			}
			return
		}
		switch got.Answer {
		case NullAnswer:
			if strings.TrimSpace(raw) != "null" || got.Targets != nil {
				t.Fatal("not literal null")
			}
		case EmptyAnswer:
			var rows []json.RawMessage
			if json.Unmarshal(original, &rows) != nil || rows == nil || len(rows) != 0 || got.Targets != nil {
				t.Fatal("not an empty array")
			}
		case TargetsAnswer:
			if len(got.Targets) == 0 || len(got.Targets) > 1024 {
				t.Fatal("invalid target count")
			}
			for _, target := range got.Targets {
				if target.URI == "" || len(target.URI) > 4096 || strings.ContainsRune(target.URI, 0) {
					t.Fatal("invalid URI")
				}
				if !target.Link && (target.Selection != target.Range || target.Origin != nil) {
					t.Fatal("invalid Location normalization")
				}
				if target.Link != got.Targets[0].Link || kind == References && target.Link {
					t.Fatal("invalid union")
				}
			}
		default:
			t.Fatal("unknown answer")
		}
	})
}
