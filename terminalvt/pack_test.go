package terminalvt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"
)

func TestPackedRowsRejectTerminalControls(t *testing.T) {
	for r := rune(0); r <= 0x9f; r++ {
		if r >= 0x20 && r < 0x7f {
			continue
		}
		t.Run(fmt.Sprintf("U+%04X", r), func(t *testing.T) {
			for _, text := range []string{string(r), "é" + string(r), string(r) + "[2J"} {
				row := []Cell{{Text: text, Width: 1}}
				if _, _, _, err := DecodeRow(nil, AppendRow(nil, row, false)); err != ErrPacked {
					t.Fatalf("plain codec accepted control text %q: %v", text, err)
				}
				var encoder RowEncoder
				packed := encoder.Append(nil, row, false)
				decoder := RowDecoder{Styles: encoder.Definitions}
				if _, _, _, err := decoder.Decode(nil, packed); err != ErrPacked {
					t.Fatalf("dictionary codec accepted control text %q: %v", text, err)
				}
			}
		})
	}
}

func TestPackedRowsPreserveSafeUnicodeFormats(t *testing.T) {
	for _, text := range []string{"é", "👩‍💻", "♻️", "❤︎", "a\u200bb", "a\u200cb", "a\u2060b"} {
		row := []Cell{{Text: text, Width: 2}}
		got, _, _, err := DecodeRow(nil, AppendRow(nil, row, false))
		if err != nil || !reflect.DeepEqual(got, row) {
			t.Fatalf("plain codec rejected safe text %q: %v", text, err)
		}
		var encoder RowEncoder
		packed := encoder.Append(nil, row, false)
		decoder := RowDecoder{Styles: encoder.Definitions}
		got, _, _, err = decoder.Decode(nil, packed)
		if err != nil || !reflect.DeepEqual(got, row) {
			t.Fatalf("dictionary codec rejected safe text %q: %v", text, err)
		}
	}
}

func TestPackedRowsRoundTripAndBounds(t *testing.T) {
	cells := []Cell{{Text: "a", Width: 1}, {Text: "界", Width: 2, FG: Color{true, 12, 34, 56}, Style: Style{Bold: true}}, {}, {Text: "é", Width: 1}, {Width: 1}}
	packed := AppendRow(nil, cells, true)
	got, wrapped, rest, err := DecodeRow(nil, packed)
	if err != nil || !wrapped || len(rest) != 0 || !reflect.DeepEqual(got, cells) {
		t.Fatalf("round trip: %#v wrapped=%t rest=%x err=%v", got, wrapped, rest, err)
	}
	for n := 0; n < len(packed); n++ {
		if _, _, _, err := DecodeRow(nil, packed[:n]); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	bad := [][]byte{{2, 0}, {0, 1, 0, 0}, {0, 1, 0, 1, 0, 3, 1, 'a'}, {0, 1, 0, 1, 0, 1, 1, 0xff}, {0, 1, 0, 1, 0, 1, 1, 0x1b}}
	// Multiple individually bounded runs must not allocate more than MaxCols.
	b := []byte{0, 2, 0}
	b = binary.AppendUvarint(b, MaxCols)
	b = append(b, bytes.Repeat([]byte{'a'}, MaxCols)...)
	b = append(b, 0, 1, 'b')
	bad = append(bad, b)
	for _, p := range bad {
		if _, _, _, err := DecodeRow(nil, p); err == nil {
			t.Fatalf("accepted malformed %x", p)
		}
	}
}

func TestPackedTextFrameBudget(t *testing.T) {
	row := make([]Cell, 200)
	for i := range row {
		row[i] = Cell{Text: string(rune('a' + i%26)), Width: 1}
	}
	packed := AppendRow(nil, row, false)
	if size := len(packed) * 50; size >= 16<<10 {
		t.Fatalf("200x50 text frame = %d bytes", size)
	}
	dst := make([]Cell, 0, 200)
	if n := testing.AllocsPerRun(100, func() {
		for range 50 {
			if _, _, _, err := DecodeRow(dst, packed); err != nil {
				panic(err)
			}
		}
	}); n != 0 {
		t.Fatalf("200x50 decode allocated %g", n)
	}
}

func BenchmarkPackedGrid(b *testing.B) {
	for _, video := range []bool{false, true} {
		name := "text"
		if video {
			name = "truecolor-video"
		}
		b.Run(name, func(b *testing.B) {
			row := make([]Cell, 200)
			for i := range row {
				row[i] = Cell{Text: "x", Width: 1}
				if video {
					row[i].FG = Color{true, uint8(i), uint8(i * 3), uint8(i * 7)}
					row[i].BG = Color{true, uint8(i * 2), uint8(i * 5), uint8(i * 11)}
				}
			}
			packed := AppendRow(nil, row, false)
			dst := make([]Cell, 0, 200)
			b.ReportAllocs()
			b.SetBytes(int64(len(packed) * 50))
			b.ResetTimer()
			for range b.N {
				for range 50 {
					if _, _, _, err := DecodeRow(dst, packed); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func FuzzPackedRow(f *testing.F) {
	f.Add(AppendRow(nil, []Cell{{Text: "x", Width: 1}, {Text: "界", Width: 2}, {}}, true))
	f.Add([]byte{0, 1, 0, 1, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		cells, wrapped, _, err := DecodeRow(nil, data)
		if err != nil {
			return
		}
		if len(cells) > MaxCols {
			t.Fatal("unbounded row")
		}
		packed := AppendRow(nil, cells, wrapped)
		round, wrap, rest, err := DecodeRow(nil, packed)
		if err != nil || wrap != wrapped || len(rest) != 0 || !reflect.DeepEqual(round, cells) {
			t.Fatalf("unstable packed row: %v", err)
		}
	})
}

func TestPackedDictionaryUnicodeRepeatLifetime(t *testing.T) {
	row := make([]Cell, 200)
	for i := range row {
		row[i] = Cell{Text: "界", Width: 2, FG: Color{true, 12, 34, 56}, Style: Style{Bold: true}}
	}
	var encoder RowEncoder
	packed := encoder.Append(nil, row, true)
	decoder := RowDecoder{Styles: encoder.Definitions}
	dst := make([]Cell, 0, len(row))
	got, wrap, rest, err := decoder.Decode(dst, packed)
	if err != nil || !wrap || len(rest) != 0 || !reflect.DeepEqual(got, row) {
		t.Fatalf("dictionary roundtrip %v", err)
	}
	if len(packed) > 32 {
		t.Fatalf("repeat did not compress: %d", len(packed))
	}
	if n := testing.AllocsPerRun(100, func() {
		if _, _, _, err := decoder.Decode(dst, packed); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("warm unicode allocated %g", n)
	}
	first := got[0].Text
	for i := range packed {
		packed[i] = 0
	}
	decoder.bytes = 1 << 20
	decoder.text([]byte("another"))
	if first != "界" {
		t.Fatal("published grapheme aliased packet or intern index")
	}
}

func TestPackedDictionaryFrameSteadyAllocationBudget(t *testing.T) {
	row := make([]Cell, 200)
	for i := range row {
		row[i] = Cell{Text: asciiText['a'+i%26], Width: 1}
	}
	var encoder RowEncoder
	packed := encoder.Append(nil, row, false)
	decoder := RowDecoder{Styles: encoder.Definitions}
	dst := make([]Cell, 0, 200)
	if len(packed)*50 >= 16<<10 {
		t.Fatal("text grid exceeds 16KB")
	}
	if n := testing.AllocsPerRun(100, func() {
		for range 50 {
			packed = encoder.Append(packed[:0], row, false)
			if _, _, _, err := decoder.Decode(dst, packed); err != nil {
				panic(err)
			}
		}
	}); n != 0 {
		t.Fatalf("steady text frame codec allocated %g", n)
	}
}

func BenchmarkPackedDictionaryGrid(b *testing.B) {
	row := make([]Cell, 200)
	for i := range row {
		row[i] = Cell{Text: asciiText['a'+i%26], Width: 1}
	}
	var encoder RowEncoder
	packed := encoder.Append(nil, row, false)
	decoder := RowDecoder{Styles: encoder.Definitions}
	dst := make([]Cell, 0, 200)
	if len(packed)*50 >= 16<<10 {
		b.Fatal("text grid exceeds wire budget")
	}
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			for range 50 {
				packed = encoder.Append(packed[:0], row, false)
			}
		}
	})
	b.Run("decode", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			for range 50 {
				if _, _, _, err := decoder.Decode(dst, packed); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

func FuzzPackedDictionaryRow(f *testing.F) {
	var encoder RowEncoder
	f.Add(encoder.Append(nil, []Cell{{Text: "界", Width: 2}, {Text: "🙂", Width: 2}, {Text: "a", Width: 1}}, false))
	f.Fuzz(func(t *testing.T, data []byte) {
		decoder := RowDecoder{Styles: []Cell{{}}}
		cells, wrapped, _, err := decoder.Decode(nil, data)
		if err != nil {
			return
		}
		if len(cells) > MaxCols {
			t.Fatal("unbounded cells")
		}
		var encoder RowEncoder
		packed := encoder.Append(nil, cells, wrapped)
		decoder.Styles = encoder.Definitions
		got, wrap, rest, err := decoder.Decode(nil, packed)
		if err != nil || wrap != wrapped || len(rest) != 0 || !reflect.DeepEqual(cells, got) {
			t.Fatal("unstable dictionary row", err)
		}
	})
}
