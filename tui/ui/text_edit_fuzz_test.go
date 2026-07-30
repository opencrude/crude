package ui

import (
	"testing"
	"unicode"
)

// FuzzTextEdit verifies TextEdit never panics and keeps cursor/value invariants
// under arbitrary insert, delete, and cursor-clamp sequences.
func FuzzTextEdit(f *testing.F) {
	f.Add("hello", 0, 0)    // insert at start
	f.Add("abc", 1, 2)      // backspace
	f.Add("xyz", 2, 1)      // delete
	f.Add("日本語", 0, 3)      // unicode insert + clamp
	f.Add("\x00\n\t", 0, 0) // filtered paste

	f.Fuzz(func(t *testing.T, text string, op, arg int) {
		e := NewTextEdit()

		// Seed with filtered printable runes only (mirrors InsertRunes contract).
		var seed []rune
		for _, r := range text {
			if unicode.IsPrint(r) && r != '\n' && r != '\t' {
				seed = append(seed, r)
			}
		}
		if len(seed) > 64 {
			seed = seed[:64]
		}
		e.SetValue(string(seed))

		switch op % 4 {
		case 0:
			e.Insert(text)
		case 1:
			e.InsertRunes(text)
		case 2:
			for i := 0; i < arg%8; i++ {
				e.Backspace()
			}
		case 3:
			for i := 0; i < arg%8; i++ {
				e.Delete()
			}
		}

		e.SetCursor(arg, arg%2 == 0)

		cur := e.Cursor()
		n := e.Len()
		if cur < 0 || cur > n {
			t.Fatalf("cursor=%d out of [0,%d] after op=%d arg=%d", cur, n, op, arg)
		}

		a, b := e.SelectionRange()
		if a < 0 || b < 0 || a > n || b > n || a > b {
			t.Fatalf("selection [%d,%d) invalid for len=%d", a, b, n)
		}

		// Value must round-trip through rune slice without surrogate issues.
		got := []rune(e.Value())
		if len(got) != n {
			t.Fatalf("Len()=%d but Value runes=%d", n, len(got))
		}
	})
}
