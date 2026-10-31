package lspaction_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"crude/lspaction"
)

// Hand-authored wire fixtures, not output from the implementation under test.
const (
	textEdit = `{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"newText":"X"}`
	edit     = `{"changes":{"file:///active.go":[` + textEdit + `]}}`
)

func parseOne(t *testing.T, item string, support bool) lspaction.Action {
	t.Helper()
	rows, err := lspaction.Parse([]byte("["+item+"]"), support)
	if err != nil || len(rows) != 1 {
		t.Fatalf("Parse = %#v, %v; want exactly one literal row", rows, err)
	}
	return rows[0]
}

func TestLiteralRows(t *testing.T) {
	first := `{"title":"Same","kind":"vendor.unrecognized.deep","isPreferred":false,"diagnostics":[{"message":"problem","code":9007199254740993}],"data":{"n":9007199254740993,"a":[null,true,"opaque"]},"x-extension":{"v":1e9999}}`
	second := `{"title":"Same","isPreferred":true,"edit":` + edit + `}`
	third := `{"title":"Unavailable","disabled":{"reason":"Requires a different source"}}`
	input := []byte("[" + first + "," + second + "," + third + "]")
	saved := bytes.Clone(input)
	rows, err := lspaction.Parse(input, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []lspaction.Action{
		{Title: "Same", Kind: "vendor.unrecognized.deep", HasPreferred: true, NeedsResolve: true, Raw: []byte(first)},
		{Title: "Same", Preferred: true, HasPreferred: true, Raw: []byte(second), Edit: []byte(edit)},
		{Title: "Unavailable", DisabledReason: "Requires a different source", UnsupportedReason: "Disabled actions cannot be applied.", Raw: []byte(third)},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("literal fields:\n got %#v\nwant %#v", rows, want)
	}
	if !bytes.Equal(input, saved) {
		t.Fatal("Parse mutated original list")
	}
	for i := range input {
		input[i] = ' '
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatal("returned rows alias caller list")
	}
	rows[1].Edit[0] = '!'
	if string(rows[1].Raw) != second || string(rows[0].Raw) != first {
		t.Fatal("Edit aliases Raw or another action")
	}
	absent := parseOne(t, `{"title":""}`, true)
	if absent.HasPreferred || absent.Preferred || !absent.NeedsResolve || absent.Title != "" {
		t.Fatalf("optional presence/empty title invented: %#v", absent)
	}
	escaped := parseOne(t, `{"ti\u0074le":"\ud83d\ude00","kind":"custom","isPreferred": false}`, true)
	if escaped.Title != "😀" || !escaped.HasPreferred || escaped.Preferred {
		t.Fatalf("valid escaped string/preferred spelling lost: %#v", escaped)
	}
}

func TestEmptyAndInvalidJSON(t *testing.T) {
	for _, raw := range []string{"null", " \r\nnull\t", "[]", " [ ] "} {
		rows, err := lspaction.Parse([]byte(raw), true)
		if err != nil || len(rows) != 0 {
			t.Errorf("%q = %#v, %v; want observed empty", raw, rows, err)
		}
	}
	bad := []string{
		"", " ", "{", "{}", "true", "0", `""`, "[", "[null]", "[[]]", "[true]",
		`[{"title":"x"},]`, `[] []`, `[{"title":"x"}]null`,
		`[{"title":"x","title":"y"}]`,
		`[{"title":"x","ti\u0074le":"y"}]`,
		`[{"title":"x","data":{"n":1,"\u006e":2}}]`,
		`[{"title":"x","data":[{"k":1,"k":2}]}]`,
		`[{"title":"x","edit":{"changes":{},"cha\u006eges":{}}}]`,
		`[{"title":"x","edit":{"changes":{"file:///a":[],"file:\/\/\/a":[]}}}]`,
		`[{"title":"\ud800"}]`, `[{"title":"\udfff"}]`,
		`[{"title":"x","data":"\ud800x"}]`,
		`[{"title":"x","data":{"\ud800":1}}]`,
		`[{"title":"x","data":NaN}]`, `[{"title":"x","data":+1}]`,
		`[{"title":"x","data":01}]`, `[{"title":"x","data":1.}]`,
		"[{\"title\":\"\xff\"}]", "[{\"title\":\"x\",\"data\":\"\xc0\x80\"}]",
	}
	for _, raw := range bad {
		t.Run(raw, func(t *testing.T) {
			rows, err := lspaction.Parse([]byte(raw), true)
			if err == nil || rows != nil {
				t.Fatalf("invalid input accepted: %#v, %v", rows, err)
			}
		})
	}
}

func TestMalformedDisplayTypes(t *testing.T) {
	items := []string{
		`{}`, `{"title":null}`, `{"title":1}`, `{"title":[]}`, `{"title":{}}`,
		`{"title":true}`, `{"title":"x","kind":null}`, `{"title":"x","kind":1}`,
		`{"title":"x","kind":[]}`, `{"title":"x","isPreferred":null}`,
		`{"title":"x","isPreferred":"false"}`, `{"title":"x","isPreferred":0}`,
		`{"title":"x","disabled":null}`, `{"title":"x","disabled":false}`,
		`{"title":"x","disabled":[]}`, `{"title":"x","disabled":{}}`,
		`{"title":"x","disabled":{"reason":null}}`,
		`{"title":"x","disabled":{"reason":2}}`,
		`{"title":"x","diagnostics":null}`, `{"title":"x","diagnostics":{}}`,
		`{"title":"x","diagnostics":[null]}`, `{"title":"x","diagnostics":[1]}`,
	}
	for _, item := range items {
		t.Run(item, func(t *testing.T) {
			raw := []byte(`[{"title":"good"},` + item + `]`)
			rows, err := lspaction.Parse(raw, true)
			if err == nil || rows != nil {
				t.Fatalf("malformed type accepted: %#v, %v", rows, err)
			}
		})
	}
}

func TestUnsupportedRowsRemainLiteral(t *testing.T) {
	cases := []struct {
		reason string
		extras []string
	}{
		{"Action has no edit and edit resolution is not supported.", []string{
			"",
		}},
		{"Bare Command actions are not supported.", []string{
			`,"command":"server.run","arguments":[{"a":1}]`,
		}},
		{"Actions containing commands are not supported.", []string{
			`,"command":{"title":"Run","command":"server.run"}`,
			`,"edit":` + edit + `,"command":{"command":"run","title":"Run"}`,
			`,"command":null`,
			`,"command":false`,
			`,"arguments":[]`,
			`,"commands":[]`,
			`,"executeCommand":"run"`,
		}},
		{"Edit operations must be inside a changes-only WorkspaceEdit.", []string{
			`,"changes":{}`,
			`,"documentChanges":[]`,
			`,"oldUri":"file:///a","newUri":"file:///b"`,
		}},
		{"Notebook actions are not supported.", []string{
			`,"notebookDocument":{}`,
		}},
		{"Disabled actions cannot be applied.", []string{
			`,"disabled":{"reason":"Not available"}`,
			`,"edit":` + edit + `,"disabled":{"reason":"Not available"}`,
			`,"disabled":{"reason":""}`,
		}},
		{"Edit must be a changes-only WorkspaceEdit object.", []string{
			`,"edit":null`,
			`,"edit":[]`,
			`,"edit":2`,
		}},
		{"Only the changes WorkspaceEdit member is supported.", []string{
			`,"edit":{}`,
			`,"edit":{"changes":{"file:///a":[` + textEdit + `]},"extension":{}}`,
		}},
		{"documentChanges are not supported.", []string{
			`,"edit":{"documentChanges":[]}`,
			`,"edit":{"changes":{"file:///a":[` + textEdit + `]},"documentChanges":[]}`,
			`,"edit":{"documentChanges":[{"textDocument":{"uri":"file:///a","version":null},"edits":[` + textEdit + `]}]}`,
			`,"edit":{"documentChanges":[{"kind":"create","uri":"file:///new"}]}`,
		}},
		{"Change annotations are not supported.", []string{
			`,"edit":{"changes":{"file:///a":[` + textEdit + `]},"changeAnnotations":{}}`,
		}},
		{"Resource operations are not supported.", []string{
			`,"edit":{"kind":"rename","oldUri":"file:///a","newUri":"file:///b"}`,
		}},
		{"Notebook edits are not supported.", []string{
			`,"edit":{"notebookDocument":{},"cells":[]}`,
		}},
		{"Edit must target exactly one nonempty URI.", []string{
			`,"edit":{"changes":{"file:///a":[` + textEdit + `],"file:///b":[]}}`,
			`,"edit":{"changes":{}}`,
			`,"edit":{"changes":{"":[` + textEdit + `]}}`,
			`,"edit":{"changes":null}`,
		}},
		{"Edit must contain at least one ordinary text edit.", []string{
			`,"edit":{"changes":{"file:///a":[]}}`,
			`,"edit":{"changes":{"file:///a":null}}`,
			`,"edit":{"changes":{"file:///a":{}}}`,
		}},
	}
	for _, tc := range cases {
		for _, extra := range tc.extras {
			item := `{"title":"Keep me"` + extra + `}`
			t.Run(item, func(t *testing.T) {
				for _, support := range []bool{false, true} {
					if extra == "" && support {
						continue
					}
					row := parseOne(t, item, support)
					if row.Title != "Keep me" || row.UnsupportedReason != tc.reason || row.NeedsResolve || string(row.Raw) != item {
						t.Fatalf("row %#v; want reason %q", row, tc.reason)
					}
					if strings.Contains(item, `"reason":"Not available"`) && row.DisabledReason != "Not available" {
						t.Fatalf("lost disabled reason: %#v", row)
					}
					if _, err := lspaction.Resolve([]byte(item), []byte(item)); err == nil {
						t.Fatal("unsupported original resolved")
					}
				}
			})
		}
	}
}

func TestStrictOrdinaryEditBoundary(t *testing.T) {
	structural := []string{
		`null`, `{}`, `{"kind":"delete","uri":"file:///a"}`,
		strings.TrimSuffix(textEdit, "}") + `,"annotationId":"a"}`,
		strings.TrimSuffix(textEdit, "}") + `,"snippet":"$0"}`,
		strings.TrimSuffix(textEdit, "}") + `,"command":null}`,
		strings.TrimSuffix(textEdit, "}") + `,"extension":1}`,
		`{"insert":{"start":{},"end":{}},"replace":{},"newText":"x"}`,
		strings.Replace(textEdit, `"newText":"X"`, `"newText":"X","insertTextFormat":2`, 1),
		strings.Replace(textEdit, `"start":{`, `"extra":1,"start":{`, 1),
		strings.Replace(textEdit, `"line":0`, `"line":0,"extension":true`, 1),
		strings.Replace(textEdit, `"line":0`, `"lineMissing":0`, 1),
	}
	for _, item := range structural {
		row := parseOne(t, `{"title":"x","edit":{"changes":{"file:///a":[`+item+`]}}}`, true)
		if row.UnsupportedReason != "Only ordinary text edits are supported." || row.NeedsResolve {
			t.Fatalf("structural edit accepted: %s => %#v", item, row)
		}
	}
	values := []string{
		strings.Replace(textEdit, `"line":0`, `"line":-1`, 1),
		strings.Replace(textEdit, `"line":0`, `"line":2147483648`, 1),
		strings.Replace(textEdit, `"line":0`, `"line":0.0`, 1),
		strings.Replace(textEdit, `"line":0`, `"line":"0"`, 1),
		strings.Replace(textEdit, `"character":1`, `"character":null`, 1),
		strings.Replace(textEdit, `"character":2`, `"character":0`, 1),
		strings.Replace(textEdit, `"newText":"X"`, `"newText":null`, 1),
		strings.Replace(textEdit, `"newText":"X"`, `"newText":2`, 1),
	}
	for _, item := range values {
		row := parseOne(t, `{"title":"x","edit":{"changes":{"file:///a":[`+item+`]}}}`, true)
		if row.UnsupportedReason != "Unsupported or malformed ordinary text edits." || row.NeedsResolve {
			t.Fatalf("invalid edit value accepted: %s => %#v", item, row)
		}
	}
	row := parseOne(t, `{"title":"x","edit":{"changes":{"not-an-authorized-uri":[`+textEdit+`]}}}`, false)
	if row.UnsupportedReason != "" || row.NeedsResolve || len(row.Edit) == 0 {
		t.Fatalf("passive parser guessed URI authority: %#v", row)
	}
}

func TestByteCountAndDepthBounds(t *testing.T) {
	assert := func(name, raw string, accepted bool) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			rows, err := lspaction.Parse([]byte(raw), true)
			if (err == nil) != accepted || !accepted && rows != nil {
				t.Fatalf("len %d: err=%v rows=%d; accepted=%t", len(raw), err, len(rows), accepted)
			}
		})
	}
	for _, field := range []struct {
		name, prefix, suffix string
		limit                int
	}{
		{"title", `{"title":"`, `"}`, 4096},
		{"kind", `{"title":"","kind":"`, `"}`, 256},
		{"disabled reason", `{"title":"","disabled":{"reason":"`, `"}}`, 4096},
	} {
		assert(field.name+" exact", "["+field.prefix+strings.Repeat("a", field.limit)+field.suffix+"]", true)
		assert(field.name+" over", "["+field.prefix+strings.Repeat("a", field.limit+1)+field.suffix+"]", false)
	}
	assert("UTF-8 title bytes exact", `[{"title":"`+strings.Repeat("😀", 1024)+`"}]`, true)
	assert("UTF-8 title bytes over", `[{"title":"`+strings.Repeat("😀", 1025)+`"}]`, false)
	escapedTitle := `[{"title":"` + strings.Repeat(`\u0061`, 4096) + `"}]`
	assert("decoded not wire title length", escapedTitle, true)
	assert("64 items", "["+strings.TrimSuffix(strings.Repeat(`{"title":"x"},`, 64), ",")+"]", true)
	assert("65 items", "["+strings.TrimSuffix(strings.Repeat(`{"title":"x"},`, 65), ",")+"]", false)
	bigTitle := `{"title":"` + strings.Repeat("a", 4096) + `"}`
	exactVisible := "[" + strings.TrimSuffix(strings.Repeat(bigTitle+",", 16), ",") + "]"
	assert("aggregate exact", exactVisible, true)
	assert("aggregate over", strings.TrimSuffix(exactVisible, "]")+`,{"title":"a"}]`, false)
	mixed := `{"title":"` + strings.Repeat("a", 4096) + `","kind":"` + strings.Repeat("b", 256) + `"}`
	assert("aggregate includes kind", "["+strings.TrimSuffix(strings.Repeat(mixed+",", 16), ",")+"]", false)
	disabled := `{"title":"` + strings.Repeat("a", 4096) + `","disabled":{"reason":"` + strings.Repeat("b", 4096) + `"}}`
	assert("aggregate includes disabled and reason", "["+strings.TrimSuffix(strings.Repeat(disabled+",", 8), ",")+"]", false)
	item := `{"title":"","data":""}`
	exactItem := strings.Replace(item, `"data":""`, `"data":"`+strings.Repeat("x", 65536-len(item))+`"`, 1)
	assert("item exact", "["+exactItem+"]", true)
	assert("item over", "["+strings.TrimSuffix(exactItem, "}")+" }]", false)
	assert("list exact", "[]"+strings.Repeat(" ", 1048576-2), true)
	assert("list over", "[]"+strings.Repeat(" ", 1048576-1), false)
	nested := func(n int) string {
		return `[{"title":"x","data":` + strings.Repeat("[", n) + "0" + strings.Repeat("]", n) + `}]`
	}
	assert("depth exact", nested(62), true)
	assert("depth over", nested(63), false)
	assert("object depth over", `[{"title":"x","data":`+strings.Repeat(`{"a":`, 63)+"null"+strings.Repeat("}", 63)+`} ]`, false)
}

func TestResolveExactOriginal(t *testing.T) {
	original := []byte(`{"title":"Fix","kind":"vendor.custom","isPreferred":false,"diagnostics":[{"message":"m","code":9007199254740993}],"data":{"huge":9007199254740993,"exp":1e9999,"a":[null,false,"opaque"]},"unknown":{"x":"value"}}`)
	resolved := []byte(` { "unknown":{"\u0078":"value"}, "data":{"a":[null,false,"opaque"],"exp":1e9999,"huge":9007199254740993},"diagnostics":[{"code":9007199254740993,"message":"m"}],"isPreferred":false,"kind":"vendor.custom","ti\u0074le":"\u0046ix", "edit":` + edit + ` } `)
	before, after := bytes.Clone(original), bytes.Clone(resolved)
	action, err := lspaction.Resolve(original, resolved)
	if err != nil {
		t.Fatal(err)
	}
	want := lspaction.Action{
		Title: "Fix", Kind: "vendor.custom", HasPreferred: true,
		Raw: bytes.Clone(resolved), Edit: []byte(edit),
	}
	if !reflect.DeepEqual(action, want) {
		t.Fatalf("resolved fields:\n got %#v\nwant %#v", action, want)
	}
	if !bytes.Equal(original, before) || !bytes.Equal(resolved, after) {
		t.Fatal("Resolve mutated its original/resolved inputs")
	}
	for i := range original {
		original[i] = ' '
	}
	for i := range resolved {
		resolved[i] = ' '
	}
	if !reflect.DeepEqual(action, want) {
		t.Fatal("resolved output aliases input")
	}
	action.Edit[0] = '!'
	if !bytes.Equal(action.Raw, after) {
		t.Fatal("resolved Edit aliases Raw")
	}
	for _, data := range []string{"null", "true", "false", `"str"`, `[]`, `{}`, `-0`, `1.00e+20`, `99999999999999999999999999999999999999999999999999999999`} {
		orig := `{"title":"x","data":` + data + `}`
		got := strings.TrimSuffix(orig, "}") + `,"edit":` + edit + `}`
		if _, err := lspaction.Resolve([]byte(orig), []byte(got)); err != nil {
			t.Errorf("exact opaque data %s: %v", data, err)
		}
	}
}

func TestResolveRejectsChangedOrAddedAttributes(t *testing.T) {
	orig := `{"title":"x","kind":"custom","isPreferred":false,"data":{"n":9007199254740993,"a":[1,2]},"diagnostics":[{"message":"m"}],"unknown":{"v":null}}`
	valid := strings.TrimSuffix(orig, "}") + `,"edit":` + edit + `}`
	bad := []struct{ name, raw string }{
		{"no change", orig},
		{"new title", strings.Replace(valid, `"title":"x"`, `"title":"y"`, 1)},
		{"new kind", strings.Replace(valid, `"kind":"custom"`, `"kind":"quickfix"`, 1)},
		{"new preference", strings.Replace(valid, `"isPreferred":false`, `"isPreferred":true`, 1)},
		{"removed preference", strings.Replace(valid, `"isPreferred":false,`, "", 1)},
		{"removed metadata", strings.Replace(valid, `,"unknown":{"v":null}`, "", 1)},
		{"changed metadata", strings.Replace(valid, `"v":null`, `"v":false`, 1)},
		{"changed diagnostics", strings.Replace(valid, `"message":"m"`, `"message":"n"`, 1)},
		{"rounded opaque number", strings.Replace(valid, "9007199254740993", "9007199254740992", 1)},
		{"reordered array", strings.Replace(valid, `"a":[1,2]`, `"a":[2,1]`, 1)},
		{"added unknown", strings.TrimSuffix(valid, "}") + `,"new":null}`},
		{"added command", strings.TrimSuffix(valid, "}") + `,"command":{"title":"x","command":"run"}}`},
		{"added null command", strings.TrimSuffix(valid, "}") + `,"command":null}`},
		{"added disabled", strings.TrimSuffix(valid, "}") + `,"disabled":{"reason":""}}`},
		{"new member instead edit", strings.TrimSuffix(orig, "}") + `,"extra":1}`},
		{"null edit", strings.TrimSuffix(orig, "}") + `,"edit":null}`},
		{"empty edit", strings.TrimSuffix(orig, "}") + `,"edit":{}}`},
		{"versioned edit", strings.TrimSuffix(orig, "}") + `,"edit":{"documentChanges":[]}}`},
		{"extra edit operation", strings.TrimSuffix(orig, "}") + `,"edit":{"changes":{"file:///a":[` + textEdit + `]},"command":"run"}}`},
		{"duplicate title", strings.TrimSuffix(valid, "}") + `,"ti\u0074le":"x"}`},
		{"duplicate nested metadata", strings.Replace(valid, `"v":null`, `"v":null,"\u0076":null`, 1)},
		{"trailing input", valid + "null"},
		{"wrong root", "null"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			before := []byte(orig)
			after := []byte(tc.raw)
			action, err := lspaction.Resolve(before, after)
			if err == nil || !reflect.DeepEqual(action, lspaction.Action{}) {
				t.Fatalf("changed resolve accepted: %#v, %v", action, err)
			}
			if string(before) != orig || string(after) != tc.raw {
				t.Fatal("rejected resolve mutated inputs")
			}
		})
	}
	for _, pair := range [][2]string{
		{"9007199254740993", "9007199254740992"}, {"1", "1.0"},
		{"1e0", "1"}, {"-0", "0"}, {"1e9999", "1e9998"},
	} {
		before := []byte(`{"title":"x","data":` + pair[0] + `}`)
		after := []byte(`{"title":"x","data":` + pair[1] + `,"edit":` + edit + `}`)
		if _, err := lspaction.Resolve(before, after); err == nil {
			t.Errorf("numeric tokens conflated: %s => %s", pair[0], pair[1])
		}
	}
	for _, field := range []string{`"isPreferred":false`, `"kind":""`, `"data":null`, `"diagnostics":[]`, `"disabled":{"reason":""}`, `"command":null`} {
		got := []byte(`{"title":"x","edit":` + edit + `,` + field + `}`)
		if _, err := lspaction.Resolve([]byte(`{"title":"x"}`), got); err == nil {
			t.Errorf("resolve invented %s", field)
		}
	}
}

func TestResolveOriginalAndWireBounds(t *testing.T) {
	validResolved := []byte(`{"title":"x","edit":` + edit + `}`)
	for _, original := range []string{
		"null", "[]", "{}", `{"title":null}`,
		`{"title":"x","edit":` + edit + `}`,
		`{"title":"x","edit":null}`,
		`{"title":"x","disabled":{"reason":"disabled"}}`,
		`{"title":"x","disabled":{"reason":""}}`,
		`{"title":"x","command":"run"}`, `{"title":"x","command":null}`,
		`{"title":"x","commands":[]}`, `{"title":"x","notebookDocument":{}}`,
		`{"title":"x","title":"x"}`, `{"title":"x","data":"\ud800"}`,
	} {
		if _, err := lspaction.Resolve([]byte(original), validResolved); err == nil {
			t.Errorf("invalid/original not resolvable: %s", original)
		}
	}
	original := []byte(`{"title":"x"}`)
	huge := append(bytes.Clone(validResolved), bytes.Repeat([]byte(" "), 65537-len(validResolved))...)
	if _, err := lspaction.Resolve(original, huge); err == nil {
		t.Fatal("oversized resolve accepted")
	}
	if _, err := lspaction.Resolve(huge, validResolved); err == nil {
		t.Fatal("oversized original accepted")
	}
	deep := []byte(`{"title":"x","data":` + strings.Repeat("[", 63) + "0" + strings.Repeat("]", 63) + `}`)
	if _, err := lspaction.Resolve(deep, validResolved); err == nil {
		t.Fatal("original depth bypassed list-equivalent budget")
	}
	badUTF8 := bytes.Replace(validResolved, []byte(`"x"`), []byte{'"', 0xff, '"'}, 1)
	if _, err := lspaction.Resolve(original, badUTF8); err == nil {
		t.Fatal("invalid UTF-8 resolution accepted")
	}
}

func FuzzLiteralActions(f *testing.F) {
	for _, seed := range [][2]string{
		{"null", "null"},
		{"[]", "{}"},
		{`[{"title":"x"}]`, `{"title":"x","edit":` + edit + `}`},
		{`{"title":"x"}`, `{"title":"x","edit":` + edit + `}`},
		{`{"title":"x","data":9007199254740993}`, `{"title":"x","data":9007199254740992,"edit":` + edit + `}`},
		{`[{"title":"x","command":null},{"title":"x","disabled":{"reason":""}}]`, "null"},
		{`[{"title":"x","data":{"a":0,"\u0061":1}}]`, "{}"},
		{`[{"title":"\ud800"}]`, "{}"},
	} {
		f.Add([]byte(seed[0]), []byte(seed[1]))
	}
	f.Fuzz(func(t *testing.T, raw, resolved []byte) {
		// Bounded campaign work independent of fuzzer allocation strategies.
		if len(raw) > 1048577 || len(resolved) > 65537 {
			return
		}
		saved, savedResolved := bytes.Clone(raw), bytes.Clone(resolved)
		for _, support := range []bool{false, true} {
			rows, err := lspaction.Parse(raw, support)
			if err != nil {
				if rows != nil {
					t.Fatal("partial rows escaped failure")
				}
				continue
			}
			if len(rows) > 64 {
				t.Fatal("item bound escaped")
			}
			visible := 0
			for _, row := range rows {
				visible += len(row.Title) + len(row.Kind) + len(row.DisabledReason) + len(row.UnsupportedReason)
				if len(row.Raw) > 65536 || !json.Valid(row.Raw) || !utf8.Valid(row.Raw) ||
					len(row.Title) > 4096 || len(row.Kind) > 256 || len(row.DisabledReason) > 4096 {
					t.Fatal("accepted row escaped wire/display bounds")
				}
				if row.Preferred && !row.HasPreferred || row.NeedsResolve && (!support || len(row.Edit) != 0 || row.UnsupportedReason != "") {
					t.Fatal("invented preferred/resolvable state")
				}
				if len(row.Edit) != 0 && !json.Valid(row.Edit) {
					t.Fatal("edit leaf is not JSON")
				}
				if row.NeedsResolve {
					got := append(bytes.Clone(bytes.TrimSpace(row.Raw[:len(row.Raw)-1])), []byte(`,"edit":`+edit+`}`)...)
					if len(got) <= 65536 {
						action, err := lspaction.Resolve(row.Raw, got)
						if err != nil || action.UnsupportedReason != "" || action.NeedsResolve || !bytes.Equal(action.Edit, []byte(edit)) {
							t.Fatalf("exact lazy item cannot add known edit: %#v, %v", action, err)
						}
					}
				}
			}
			if visible > 65536 {
				t.Fatal("aggregate visible bound escaped")
			}
		}
		action, err := lspaction.Resolve(raw, resolved)
		if err == nil && (action.UnsupportedReason != "" || action.NeedsResolve || len(action.Edit) == 0 || !bytes.Equal(action.Raw, resolved)) {
			t.Fatal("resolution accepted an unsupported/aliased shape")
		}
		if !bytes.Equal(raw, saved) || !bytes.Equal(resolved, savedResolved) {
			t.Fatal("input mutated")
		}
	})
}
