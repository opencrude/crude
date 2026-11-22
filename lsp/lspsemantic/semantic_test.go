package lspsemantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestFullAndRangeDecodeUTF16SourceWithoutChangingIt(t *testing.T) {
	source := "a😀x\r\n界 name\n"
	legend := Legend{Types: []string{"keyword", "parameter", "custom"}, Modifiers: []string{"declaration", "readonly"}}
	// a / 😀 / x, then 界 / name. Deltas are relative to each token's start.
	result, err := ParseFull([]byte(`{"resultId":"v1","data":[0,0,1,0,0,0,1,2,1,1,0,2,1,2,0,1,0,1,2,0,0,2,4,1,2]}`))
	if err != nil {
		t.Fatal(err)
	}
	spans, err := Decode(source, legend, result.Data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Span{{0, 1, "keyword", 0}, {1, 5, "parameter", 1}, {5, 6, "custom", 0}, {8, 11, "custom", 0}, {12, 16, "parameter", 2}}
	if !reflect.DeepEqual(spans, want) {
		t.Fatalf("decoded spans %+v, want %+v", spans, want)
	}
	if source != "a😀x\r\n界 name\n" {
		t.Fatal("token observation changed source")
	}
	empty, err := ParseFull([]byte(`{"data":[]}`))
	if err != nil || empty.Null || len(empty.Data) != 0 {
		t.Fatal(empty, err)
	}
	null, err := ParseFull([]byte(`null`))
	if err != nil || !null.Null {
		t.Fatal(null, err)
	}
}

func TestSemanticTokensRejectMalformedAndNonSourceRanges(t *testing.T) {
	legend := Legend{Types: []string{"variable"}, Modifiers: []string{"declaration"}}
	for name, raw := range map[string]string{
		"duplicate": `{"data":[],"data":[]}`, "null data": `{"data":null}`, "negative": `{"data":[-1,0,1,0,0]}`,
		"fraction": `{"data":[0,0,1.0,0,0]}`, "too large": `{"data":[2147483648,0,1,0,0]}`,
		"incomplete": `{"data":[0,0,1,0]}`, "bad id": `{"data":[],"resultId":null}`,
		"null integer": `{"data":[0,0,null,0,0]}`, "unpaired surrogate": `{"data":[],"resultId":"\ud800"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFull([]byte(raw)); err == nil {
				t.Fatal("malformed token evidence accepted")
			}
		})
	}
	for name, data := range map[string][]uint32{
		"zero length": {0, 0, 0, 0, 0}, "type index": {0, 0, 1, 1, 0}, "modifier bit": {0, 0, 1, 0, 2},
		"surrogate start": {0, 2, 1, 0, 0}, "surrogate end": {0, 1, 1, 0, 0}, "line past end": {9, 0, 1, 0, 0},
		"column past end": {0, 4, 1, 0, 0}, "overlap": {0, 0, 3, 0, 0, 0, 1, 2, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode("a😀b\r\n", legend, data); err == nil {
				t.Fatal("invalid source coordinates accepted")
			}
		})
	}
	clipped, err := Decode("a😀b\r\n", legend, []uint32{0, 3, 2, 0, 0})
	if err != nil || !reflect.DeepEqual(clipped, []Span{{5, 6, "variable", 0}}) {
		t.Fatalf("single-line client did not clip a token at the line end: %+v %v", clipped, err)
	}
	for _, bad := range []Legend{{}, {Types: []string{""}}, {Types: []string{strings.Repeat("x", MaxLegendStringBytes+1)}}, {Types: []string{"x"}, Modifiers: make([]string, 32)}} {
		if _, err := Decode("x", bad, []uint32{0, 0, 1, 0, 0}); err == nil {
			t.Fatal("invalid legend accepted", bad)
		}
	}
}

func TestDeltaUsesOriginalIndexesAndCanReplaceWithFull(t *testing.T) {
	base := []uint32{0, 0, 1, 0, 0, 0, 2, 1, 0, 0, 0, 2, 1, 0, 0}
	result, err := ParseDelta([]byte(`{"resultId":"v2","edits":[{"start":0,"deleteCount":5,"data":[0,1,1,0,0]},{"start":10,"deleteCount":5}]}`), base)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{0, 1, 1, 0, 0, 0, 2, 1, 0, 0}
	if !reflect.DeepEqual(result.Data, want) || result.ID != "v2" {
		t.Fatal(result)
	}
	unsorted, err := ParseDelta([]byte(`{"resultId":"v2","edits":[{"start":10,"deleteCount":5},{"start":0,"deleteCount":5,"data":[0,1,1,0,0]}]}`), base)
	if err != nil || !reflect.DeepEqual(unsorted.Data, want) {
		t.Fatalf("standard unordered delta was rejected: %+v %v", unsorted, err)
	}
	inserts, err := ParseDelta([]byte(`{"edits":[{"start":5,"deleteCount":0,"data":[1,2]},{"start":5,"deleteCount":0,"data":[3,4,5]}]}`), base)
	if err != nil || !reflect.DeepEqual(inserts.Data[5:10], []uint32{1, 2, 3, 4, 5}) {
		t.Fatalf("same-offset insert order lost: %+v %v", inserts, err)
	}
	if base[1] != 0 || len(base) != 15 {
		t.Fatal("delta mutated the retained base")
	}
	full, err := ParseDelta([]byte(`{"resultId":"fresh","data":[0,0,1,0,0]}`), base)
	if err != nil || full.ID != "fresh" || len(full.Data) != 5 {
		t.Fatal(full, err)
	}
	for name, raw := range map[string]string{
		"overlap":                              `{"edits":[{"start":0,"deleteCount":6},{"start":4,"deleteCount":1}]}`,
		"same-start replacement before insert": `{"edits":[{"start":1,"deleteCount":1},{"start":1,"deleteCount":0}]}`,
		"out of bounds":                        `{"edits":[{"start":14,"deleteCount":2}]}`,
		"fraction":                             `{"edits":[{"start":1.5,"deleteCount":0}]}`,
		"null data":                            `{"edits":[{"start":0,"deleteCount":0,"data":null}]}`,
		"ambiguous":                            `{"data":[],"edits":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDelta([]byte(raw), base); err == nil {
				t.Fatal("invalid delta accepted")
			}
		})
	}
}

func TestSemanticEmptyLegendAndCancellation(t *testing.T) {
	spans, err := Decode("text", Legend{}, nil)
	if err != nil || len(spans) != 0 {
		t.Fatal("empty legend should permit an empty result", spans, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DecodeContext(ctx, "text", Legend{Types: []string{"variable"}}, []uint32{0, 0, 1, 0, 0}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled decode did not stop", err)
	}
}
func TestSemanticIntegerBoundIsCheckedDuringDecoding(t *testing.T) {
	raw := []byte(`{"data":[` + strings.Repeat("0,", MaxDataInts) + `0]}`)
	if _, err := ParseFull(raw); err == nil {
		t.Fatal("oversized integer vector accepted")
	}
}
func FuzzSemanticEvidenceNeverPanics(f *testing.F) {
	for _, raw := range []string{`null`, `{"data":[]}`, `{"data":[0,0,1,0,0]}`, `{"edits":[{"start":0,"deleteCount":0}]}`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		full, err := ParseFull(raw)
		if err == nil && !full.Null {
			_, _ = Decode("a😀b\r\n", Legend{Types: []string{"variable"}}, full.Data)
		}
		delta, err := ParseDelta(raw, []uint32{0, 0, 1, 0, 0})
		if err == nil && !delta.Null {
			_, _ = Decode("a😀b\r\n", Legend{Types: []string{"variable"}}, delta.Data)
		}
	})
}
func BenchmarkSemanticDecodeDense(b *testing.B) {
	const lines = 20000
	source := strings.Repeat("name value\n", lines)
	data := make([]uint32, 0, lines*5)
	for i := 0; i < lines; i++ {
		delta := uint32(1)
		if i == 0 {
			delta = 0
		}
		data = append(data, delta, 0, 4, 0, 0)
	}
	legend := Legend{Types: []string{"variable"}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		spans, err := Decode(source, legend, data)
		if err != nil || len(spans) != lines {
			b.Fatal(err)
		}
	}
}
func BenchmarkSemanticParseDense(b *testing.B) {
	data := make([]uint32, 0, 100000)
	for i := 0; i < 20000; i++ {
		data = append(data, 1, 0, 4, 0, 0)
	}
	raw, err := json.Marshal(struct {
		Data []uint32 `json:"data"`
	}{data})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result, err := ParseFull(raw)
		if err != nil || len(result.Data) != len(data) {
			b.Fatal(fmt.Sprint(err))
		}
	}
}
