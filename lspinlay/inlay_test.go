package lspinlay_test

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"crude/lspinlay"
)

const (
	pos   = `{"line":0,"character":2}`
	span  = `{"start":{"line":0,"character":2},"end":{"line":1,"character":0}}`
	basic = `{"position":{"line":0,"character":2},"label":"x"}`
)

func accept(t testing.TB, raw string) lspinlay.Result {
	t.Helper()
	result, err := lspinlay.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	return result
}

func reject(t testing.TB, raw string) {
	t.Helper()
	result, err := lspinlay.Parse([]byte(raw))
	if err == nil || !reflect.DeepEqual(result, lspinlay.Result{}) {
		t.Fatalf("expected atomic refusal, got %+v / %v", result, err)
	}
}

func item(label, extra string) string {
	return `{"position":` + pos + `,"label":` + label + extra + `}`
}

func TestLiteralNormalization(t *testing.T) {
	for raw, answer := range map[string]string{"null": "null", " \nnull\t": "null", "[]": "empty", "[ \n ]": "empty"} {
		got := accept(t, raw)
		if got.Answer != answer || len(got.Hints) != 0 {
			t.Fatalf("%q: %+v", raw, got)
		}
	}
	first := `{"position":{"line":0,"character":2},"label":"type","kind":1,"paddingLeft":true,"paddingRight":false,"tooltip":"","textEdits":[],"data":null}`
	second := `{"position":{"line":0,"character":2},"label":[{"value":"e","tooltip":{"kind":"markdown","value":"**part**"},"location":{"uri":"opaque:remote/path","range":{"start":{"line":0,"character":2},"end":{"line":1,"character":0}}},"command":{"title":"Never execute","command":"extension.any","arguments":[null,9007199254740993,1e9999]}},{"value":"\u0301"},{"value":"👩"},{"value":"\u200d💻"}],"kind":2,"tooltip":{"kind":"plaintext","value":"literal"}}`
	raw := "[" + first + "," + second + "," + basic + "]"
	got := accept(t, raw)
	if got.Answer != "hints" || len(got.Hints) != 3 {
		t.Fatalf("unexpected result: %+v", got)
	}
	a, b, c := got.Hints[0], got.Hints[1], got.Hints[2]
	if a.Label != "type" || a.LabelIsParts || a.Position != (lspinlay.Position{Line: 0, Character: 2}) ||
		a.Kind != 1 || !a.HasKind || !a.PaddingLeft || a.PaddingRight || !a.HasData || !a.HasTextEdits ||
		a.Tooltip != (lspinlay.Markup{Present: true, Form: "string"}) {
		t.Fatalf("plain literal lost metadata: %+v", a)
	}
	if b.Label != "é👩‍💻" || !b.LabelIsParts || len(b.Parts) != 4 || b.Parts[1].Value != "́" ||
		b.Parts[3].Value != "\u200d💻" || b.Kind != 2 || !b.HasKind ||
		b.Tooltip != (lspinlay.Markup{Present: true, Form: "markup", Kind: "plaintext", Value: "literal"}) {
		t.Fatalf("parts changed or segmented: %+v", b)
	}
	p := b.Parts[0]
	if p.Value != "e" || !p.HasCommand || p.Tooltip != (lspinlay.Markup{Present: true, Form: "markup", Kind: "markdown", Value: "**part**"}) ||
		p.Location == nil || p.Location.URI != "opaque:remote/path" ||
		p.Location.Range != (lspinlay.Range{Start: lspinlay.Position{Line: 0, Character: 2}, End: lspinlay.Position{Line: 1}}) {
		t.Fatalf("lost part metadata: %+v", p)
	}
	if c.Label != "x" || c.HasKind || c.HasTextEdits || c.HasData || c.Tooltip.Present || len(c.Parts) != 0 {
		t.Fatalf("absence inferred: %+v", c)
	}
	for i, want := range []string{first, second, basic} {
		if string(got.Hints[i].Raw) != want {
			t.Fatalf("item %d lost exact JSON/order", i)
		}
	}
	accept(t, `[{"position":{"line":2147483647,"character":2147483647},"label":"\ud83d\ude00"}]`)
	// Controls are literal data for a later escaped display policy.
	if h := accept(t, "["+item(`"\u0000\r\n\t\u001b[31m"`, "")+"]").Hints[0]; h.Label != "\x00\r\n\t\x1b[31m" {
		t.Fatal("control label was changed")
	}
}

func TestOpaqueRawAndMutationIsolation(t *testing.T) {
	literal := `{"position":{"line":0,"character":2},"label":[{"value":"x","command":{"title":"","command":"","arguments":[{"big":1234567890123456789012345678901234567890,"exp":-2.50e+9999}]},"location":{"uri":"../not-authorized","range":` + span + `}}],"data":{"big":9007199254740993,"n":1.00E-4000,"a":[null,false,{"x":true}]},"extension":{"tooltip":"inert","escaped":"\\ud800"}}`
	input := []byte("[" + literal + "," + literal + "]")
	got, err := lspinlay.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	for i := range input {
		input[i] = ' '
	}
	if string(got.Hints[0].Raw) != literal || string(got.Hints[1].Raw) != literal {
		t.Fatal("input mutation changed retained raw")
	}
	got.Hints[0].Raw[0] = '!'
	got.Hints[0].Parts[0].Location.URI = "changed"
	got.Hints[0].Parts[0].Value = "changed"
	if string(got.Hints[1].Raw) != literal || got.Hints[1].Parts[0].Location.URI != "../not-authorized" ||
		got.Hints[1].Parts[0].Value != "x" {
		t.Fatal("one result aliases another")
	}
	again := accept(t, "["+literal+"]")
	if string(again.Hints[0].Raw) != literal {
		t.Fatal("caller mutation reached later parse")
	}
}

func TestMalformedAndKnownSchemas(t *testing.T) {
	bad := []string{
		"", " ", "null null", "[] garbage", "[", "{}", "true", `"x"`, "123", "[null]", "[true]", "[[]]",
		`[{"label":"x"}]`, `[{"position":` + pos + `}]`, `[{"position":null,"label":"x"}]`,
		`[{"position":{"line":0},"label":"x"}]`, `[{"position":{"character":0},"label":"x"}]`,
		"[" + item(`null`, "") + "]", "[" + item(`[]`, "") + "]", "[" + item(`123`, "") + "]",
		"[" + item(`""`, "") + "]", "[" + item(`[{"value":""}]`, "") + "]",
		"[" + item(`[{"value":"x"},{"value":""}]`, "") + "]",
		"[" + item(`[null]`, "") + "]", "[" + item(`[{}]`, "") + "]", "[" + item(`[{"value":false}]`, "") + "]",
		"[" + item(`[{"value":null}]`, "") + "]", "[" + item(`"x"`, `,"kind":3`) + "]",
		"[" + item(`"x"`, `,"kind":0`) + "]", "[" + item(`"x"`, `,"kind":"1"`) + "]",
		"[" + item(`"x"`, `,"kind":1.0`) + "]", "[" + item(`"x"`, `,"paddingLeft":0`) + "]",
		"[" + item(`"x"`, `,"paddingRight":"false"`) + "]",
		"[" + item(`"x"`, `,"tooltip":false`) + "]",
		"[" + item(`"x"`, `,"tooltip":{"kind":"html","value":"x"}`) + "]",
		"[" + item(`"x"`, `,"tooltip":{"kind":"plaintext"}`) + "]",
		"[" + item(`"x"`, `,"tooltip":{"value":"x"}`) + "]",
		"[" + item(`"x"`, `,"tooltip":{"kind":"markdown","value":null}`) + "]",
		"[" + item(`"x"`, `,"textEdits":{}`) + "]",
		"[" + item(`"x"`, `,"textEdits":[null]`) + "]",
		"[" + item(`"x"`, `,"textEdits":[{"range":`+span+`}]`) + "]",
		"[" + item(`"x"`, `,"textEdits":[{"newText":"x"}]`) + "]",
		"[" + item(`"x"`, `,"textEdits":[{"range":`+span+`,"newText":null}]`) + "]",
		"[" + item(`[{"value":"x","location":null}]`, "") + "]",
		"[" + item(`[{"value":"x","location":{"uri":"x"}}]`, "") + "]",
		"[" + item(`[{"value":"x","location":{"uri":null,"range":`+span+`}}]`, "") + "]",
		"[" + item(`[{"value":"x","location":{"uri":"x","range":null}}]`, "") + "]",
		"[" + item(`[{"value":"x","command":null}]`, "") + "]",
		"[" + item(`[{"value":"x","command":"run"}]`, "") + "]",
		"[" + item(`[{"value":"x","command":{"title":"x"}}]`, "") + "]",
		"[" + item(`[{"value":"x","command":{"command":"x"}}]`, "") + "]",
		"[" + item(`[{"value":"x","command":{"title":false,"command":"x"}}]`, "") + "]",
		"[" + item(`[{"value":"x","command":{"title":"x","command":"x","arguments":null}}]`, "") + "]",
		"[" + item(`[{"value":"x","command":{"title":"x","command":"x","arguments":{}}}]`, "") + "]",
	}
	for _, name := range []string{"kind", "paddingLeft", "paddingRight", "tooltip", "textEdits"} {
		bad = append(bad, "["+item(`"x"`, `,"`+name+`":null`)+"]")
	}
	for _, n := range []string{"-0", "-1", "1.0", "0e0", "1E0", "2147483648", "999999999999999999999999", `"0"`, "null", "true", "00", "+1"} {
		for _, axis := range []string{"line", "character"} {
			bad = append(bad, strings.Replace("["+basic+"]", `"`+axis+`":`+map[string]string{"line": "0", "character": "2"}[axis], `"`+axis+`":`+n, 1))
		}
	}
	for i, raw := range bad {
		t.Run(fmt.Sprint(i), func(t *testing.T) { reject(t, raw) })
	}
	// Even a valid prefix cannot leak a partial result.
	reject(t, "["+basic+",null]")
	validEdit := item(`"x"`, `,"textEdits":[{"range":`+span+`,"newText":"\r\n界"}]`)
	h := accept(t, "["+validEdit+"]").Hints[0]
	if !h.HasTextEdits {
		t.Fatal("valid inert textEdit lost")
	}
	for _, reversed := range []string{
		`{"start":{"line":1,"character":0},"end":{"line":0,"character":9}}`,
		`{"start":{"line":0,"character":3},"end":{"line":0,"character":2}}`,
	} {
		reject(t, "["+strings.Replace(validEdit, span, reversed, 1)+"]")
		reject(t, "["+item(`[{"value":"x","location":{"uri":"x","range":`+reversed+`}}]`, "")+"]")
	}
}

func TestUnicodeAndDuplicatesAtNestedDepths(t *testing.T) {
	for _, escaped := range []string{`\ud800`, `\udfff`, `\ud800x`, `\ud800\u0041`, `\ud800\ud800`} {
		reject(t, "["+item(`"`+escaped+`"`, "")+"]")
		reject(t, "["+item(`"x"`, `,"data":{"`+escaped+`":true}`)+"]")
	}
	reject(t, "["+item("\""+string([]byte{0xff})+"\"", "")+"]")
	reject(t, "["+item(`"x"`, `,"data":"`+string([]byte{0xed, 0xa0, 0x80})+`"`)+"]")
	reject(t, "["+item("\"a\nb\"", "")+"]")
	for _, raw := range []string{
		`[{"position":` + pos + `,"label":"x","\u006cabel":"y"}]`,
		`[{"position":{"line":0,"line":0,"character":2},"label":"x"}]`,
		"[" + item(`[{"value":"x","value":"x"}]`, "") + "]",
		"[" + item(`"x"`, `,"tooltip":{"kind":"plaintext","value":"x","value":"y"}`) + "]",
		"[" + item(`"x"`, `,"data":{"a":[{"x":1,"\u0078":2}]}`) + "]",
		"[" + item(`"x"`, `,"extension":{"nested":{"a":1,"a":1}}`) + "]",
		"[" + item(`[{"value":"x","command":{"title":"x","command":"x","arguments":[{"a":1,"a":2}]}}]`, "") + "]",
		"[" + item(`"x"`, `,"textEdits":[{"range":{"start":`+pos+`,"end":`+pos+`,"end":`+pos+`},"newText":""}]`) + "]",
	} {
		reject(t, raw)
	}
	accept(t, "["+item(`"\uFFFD\ud83d\ude00\\ud800\"ok"`, `,"data":{"valid":"\u0000"}`)+"]")
}

func TestBounds(t *testing.T) {
	if lspinlay.MaxRawBytes != 1<<20 || lspinlay.MaxHints != 1024 || lspinlay.MaxParts != 32 ||
		lspinlay.MaxLabelBytes != 4096 || lspinlay.MaxDisplayBytes != 64<<10 || lspinlay.MaxDepth != 64 {
		t.Fatal("assigned public limits changed")
	}
	// Max label bytes, both decoded escapes and multibyte labels.
	for _, label := range []string{strings.Repeat("a", 4096), strings.Repeat(`\u0061`, 4096), strings.Repeat("é", 2048)} {
		accept(t, "["+item(`"`+label+`"`, "")+"]")
		reject(t, "["+item(`"`+label+`x"`, "")+"]")
	}
	for _, n := range []int{32, 33} {
		raw := "[" + item("["+strings.TrimSuffix(strings.Repeat(`{"value":"a"},`, n), ",")+"]", "") + "]"
		if n == 32 {
			accept(t, raw)
		} else {
			reject(t, raw)
		}
	}
	parts := `[{"value":"` + strings.Repeat("a", 2048) + `"},{"value":"` + strings.Repeat("b", 2048) + `"}]`
	accept(t, "["+item(parts, "")+"]")
	reject(t, "["+item(strings.Replace(parts, `"b`, `"bb`, 1), "")+"]")
	many := func(entry string, n int) string {
		return "[" + strings.TrimSuffix(strings.Repeat(entry+",", n), ",") + "]"
	}
	accept(t, many(basic, 1024))
	reject(t, many(basic, 1025))
	long := item(`"`+strings.Repeat("x", 4096)+`"`, "")
	accept(t, many(long, 16))
	reject(t, many(long, 17))
	// Containers: response array + hint object + 62 nested data arrays = 64.
	for _, n := range []int{62, 63} {
		h := item(`"x"`, `,"data":`+strings.Repeat("[", n)+"0"+strings.Repeat("]", n))
		if n == 62 {
			accept(t, "["+h+"]")
			if _, err := lspinlay.Resolve([]byte(h), []byte(h)); err != nil {
				t.Fatal(err)
			}
		} else {
			reject(t, "["+h+"]")
			if _, err := lspinlay.Resolve([]byte(h), []byte(h)); err == nil {
				t.Fatal("resolve did not reserve list depth")
			}
		}
	}
	for _, name := range []string{"tooltip", "data", "extension"} {
		at := item(`"x"`, `,"`+name+`":"`+strings.Repeat("m", 64<<10)+`"`)
		accept(t, "["+at+"]")
		reject(t, "["+strings.Replace(at, `"mmm`, `"mmmm`, 1)+"]")
	}
	for _, extra := range []string{
		`,"data":` + many("0", 1024),
		`,"textEdits":` + many(`{"range":`+span+`,"newText":""}`, 1024),
	} {
		accept(t, "["+item(`"x"`, extra)+"]")
		reject(t, "["+item(`"x"`, strings.Replace(extra, "]", ",null]", 1))+"]")
	}
	args := `[{"value":"x","command":{"title":"x","command":"x","arguments":` + many("null", 1024) + `}}]`
	accept(t, "["+item(args, "")+"]")
	reject(t, "["+item(strings.Replace(args, "null]", "null,null]", 1), "")+"]")
	members := make([]string, 1024)
	for i := range members {
		members[i] = fmt.Sprintf(`"k%d":null`, i)
	}
	accept(t, "["+item(`"x"`, `,"data":{`+strings.Join(members, ",")+`}`)+"]")
	reject(t, "["+item(`"x"`, `,"data":{"extra":null,`+strings.Join(members, ",")+`}`)+"]")
	accept(t, "["+item(`"x"`, `,"data":{"`+strings.Repeat("k", 64<<10)+`":0}`)+"]")
	reject(t, "["+item(`"x"`, `,"data":{"`+strings.Repeat("k", (64<<10)+1)+`":0}`)+"]")
	padded := "[]" + strings.Repeat(" ", (1<<20)-2)
	accept(t, padded)
	reject(t, padded+" ")
	// A single item also obeys the raw limit in Resolve, counting outer whitespace.
	one := basic + strings.Repeat(" ", (1<<20)-len(basic))
	if _, err := lspinlay.Resolve([]byte(one), []byte(one)); err != nil {
		t.Fatal(err)
	}
	if _, err := lspinlay.Resolve([]byte(one+" "), []byte(one)); err == nil {
		t.Fatal("oversized original resolve accepted")
	}
	if _, err := lspinlay.Resolve([]byte(one), []byte(one+" ")); err == nil {
		t.Fatal("oversized resolved item accepted")
	}
}

func TestTooltipOnlyResolve(t *testing.T) {
	before := `{"position":{"line":0,"character":2},"label":[{"value":"e"},{"value":"\u0301","tooltip":"old","command":{"title":"keep","command":"keep","arguments":[1.00,9007199254740993,1e9999]}}],"data":{"n":-0,"v":1e+2},"textEdits":[],"paddingLeft":false,"extension":{"tooltip":"immutable"}}`
	after := ` { "extension":{"tooltip":"immutable"},"paddingLeft":false,"textEdits":[],"data":{"v":1e+2,"n":-0},"label":[{"value":"\u0065","tooltip":{"kind":"markdown","value":"new"}},{"command":{"arguments":[1.00,9007199254740993,1e9999],"command":"keep","title":"keep"},"tooltip":"","value":"́"}],"position":{"character":2,"line":0},"tooltip":{"kind":"plaintext","value":"top"} } `
	resolved := []byte(after)
	hint, err := lspinlay.Resolve([]byte(before), resolved)
	if err != nil {
		t.Fatal(err)
	}
	if string(hint.Raw) != after || hint.Label != "é" || hint.Tooltip.Value != "top" ||
		hint.Parts[0].Tooltip.Kind != "markdown" || !hint.Parts[1].Tooltip.Present ||
		hint.Parts[1].Tooltip.Form != "string" {
		t.Fatalf("incorrect resolved evidence: %+v", hint)
	}
	resolved[0] = '!'
	if string(hint.Raw) != after {
		t.Fatal("resolve raw aliases argument")
	}
	if _, err := lspinlay.Resolve([]byte(basic), []byte(basic)); err != nil {
		t.Fatal("no-op tooltip resolution must be valid:", err)
	}
	if _, err := lspinlay.Resolve([]byte(item(`"x"`, `,"tooltip":"old"`)),
		[]byte(item(`"x"`, `,"tooltip":{"kind":"markdown","value":"new"}`))); err != nil {
		t.Fatal("top-level tooltip change rejected:", err)
	}
	changes := []struct{ old, new string }{
		{`"character":2`, `"character":3`},
		{`"paddingLeft":false`, `"paddingLeft":true`},
		{`,"paddingLeft":false`, ``},
		{`"textEdits":[]`, `"textEdits":[{"range":` + span + `,"newText":""}]`},
		{`"title":"keep"`, `"title":"changed"`},
		{`"arguments":[1.00`, `"arguments":[1.0`},
		{`9007199254740993`, `9007199254740992`},
		{`1e9999`, `1E9999`},
		{`"n":-0`, `"n":0`},
		{`"v":1e+2`, `"v":100`},
		{`"value":"e"`, `"value":"E"`},
		{`"extension":{"tooltip":"immutable"}`, `"extension":{"tooltip":"changed"}`},
		{`,"extension":{"tooltip":"immutable"}`, ``},
		{`"tooltip":"old",`, ``},
		{`"tooltip":"old"`, `"tooltip":null`},
		{`"data":`, `"extra":null,"data":`},
		{`"label":`, `"kind":1,"label":`},
		{`"value":"e"`, `"value":"e","location":{"uri":"x","range":` + span + `}`},
	}
	for i, change := range changes {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			changed := strings.Replace(before, change.old, change.new, 1)
			if changed == before {
				t.Fatal("invalid test fixture")
			}
			if h, err := lspinlay.Resolve([]byte(before), []byte(changed)); err == nil || !reflect.DeepEqual(h, lspinlay.Hint{}) {
				t.Fatalf("forbidden change accepted: %+v %v", h, err)
			}
		})
	}
	for _, pair := range [][2]string{
		{item(`"x"`, `,"tooltip":""`), basic},
		{basic, item(`[{"value":"x"}]`, "")},
		{item(`[{"value":"a"},{"value":"b"}]`, ""), item(`[{"value":"b"},{"value":"a"}]`, "")},
		{item(`[{"value":"x"}]`, ""), item(`[{"value":"x"},{"value":""}]`, "")},
		{basic, item(`"x"`, `,"data":null`)},
		{item(`"x"`, `,"data":null`), basic},
		{`{"position":` + pos + `,"label":"x","label":"x"}`, basic},
		{basic, `{"position":` + pos + `,"label":"x","tooltip":"\ud800"}`},
		{"null", basic}, {basic, "[" + basic + "]"},
	} {
		if _, err := lspinlay.Resolve([]byte(pair[0]), []byte(pair[1])); err == nil {
			t.Fatalf("invalid resolve accepted: %v", pair)
		}
	}
}

func FuzzInlay(f *testing.F) {
	for _, seed := range [][2]string{
		{"null", basic}, {"[]", basic}, {"[" + basic + "]", basic},
		{basic, item(`"x"`, `,"tooltip":"new"`)},
		{item(`[{"value":"e"},{"value":"\u0301"}]`, `,"data":[9007199254740993,1e9999]`),
			item(`[{"value":"e","tooltip":"new"},{"value":"\u0301"}]`, `,"data":[9007199254740993,1e9999]`)},
		{`[{"position":` + pos + `,"label":"\ud800"}]`, "null"},
	} {
		f.Add([]byte(seed[0]), []byte(seed[1]))
	}
	f.Fuzz(func(t *testing.T, raw, replacement []byte) {
		result, err := lspinlay.Parse(raw)
		if err != nil {
			if !reflect.DeepEqual(result, lspinlay.Result{}) {
				t.Fatal("partial parse result")
			}
		} else {
			for _, hint := range result.Hints {
				round := accept(t, "["+string(hint.Raw)+"]")
				if !reflect.DeepEqual(round.Hints[0], hint) {
					t.Fatal("raw and normalized hint disagree")
				}
				if _, err := lspinlay.Resolve(hint.Raw, hint.Raw); err != nil {
					t.Fatal("valid hint cannot resolve to itself:", err)
				}
			}
		}
		hint, err := lspinlay.Resolve(raw, replacement)
		if err != nil {
			if !reflect.DeepEqual(hint, lspinlay.Hint{}) {
				t.Fatal("partial resolve result")
			}
			return
		}
		if !bytes.Equal(hint.Raw, replacement) {
			t.Fatal("resolved raw reconstructed")
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed)+2 > lspinlay.MaxRawBytes {
			return // Resolve counts item bytes; Parse also counts its list wrapper.
		}
		before := accept(t, "["+string(trimmed)+"]").Hints[0]
		if before.Label != hint.Label || before.Position != hint.Position || before.Kind != hint.Kind ||
			before.HasKind != hint.HasKind || before.PaddingLeft != hint.PaddingLeft || before.PaddingRight != hint.PaddingRight ||
			before.HasData != hint.HasData || before.HasTextEdits != hint.HasTextEdits {
			t.Fatal("resolve changed immutable normalized content")
		}
	})
}
