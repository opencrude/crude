package ui

import (
	"testing"
)

func TestTextEdit_InsertCursorBackspace(t *testing.T) {
	e := NewTextEdit()
	e.Insert("hi")
	if e.Value() != "hi" || e.Cursor() != 2 {
		t.Fatalf("%q cur=%d", e.Value(), e.Cursor())
	}
	e.Backspace()
	if e.Value() != "h" || e.Cursor() != 1 {
		t.Fatal(e.Value(), e.Cursor())
	}
	e.Insert("ello")
	if e.Value() != "hello" {
		t.Fatal(e.Value())
	}
}

func TestTextEdit_UnicodeRuneSafe(t *testing.T) {
	e := NewTextEdit()
	e.Insert("日本語")
	if e.Len() != 3 || e.Cursor() != 3 {
		t.Fatalf("len=%d cur=%d", e.Len(), e.Cursor())
	}
	e.Backspace()
	if e.Value() != "日本" || e.Cursor() != 2 {
		t.Fatalf("%q cur=%d", e.Value(), e.Cursor())
	}
	e.MoveLeft(false)
	e.Delete()
	if e.Value() != "日" {
		t.Fatal(e.Value())
	}
}

func TestTextEdit_SelectionReplace(t *testing.T) {
	e := NewTextEdit()
	e.SetValue("abcdef")
	e.SetCursor(1, false)
	e.SetCursor(4, true) // select bcd
	if e.SelectedText() != "bcd" {
		t.Fatal(e.SelectedText())
	}
	e.Insert("X")
	if e.Value() != "aXef" {
		t.Fatal(e.Value())
	}
}

func TestTextEdit_WordMotion(t *testing.T) {
	e := NewTextEdit()
	e.SetValue("foo bar  baz")
	e.MoveEnd(false)
	e.MoveWordLeft(false)
	if e.Cursor() != 9 { // start of baz
		// "foo bar  baz" — baz starts at 9
		if string([]rune(e.Value())[e.Cursor():]) != "baz" {
			t.Fatalf("cur=%d rest=%q", e.Cursor(), string([]rune(e.Value())[e.Cursor():]))
		}
	}
	e.MoveHome(false)
	e.MoveWordRight(false)
	if e.Cursor() != 4 { // after foo+space → start of bar? after word+spaces
		// MoveWordRight from 0: skip "foo", skip space → at "bar" (4)
		if e.Cursor() != 4 {
			t.Fatalf("cur=%d", e.Cursor())
		}
	}
}

func TestTextEdit_SelectAllAndLineOps(t *testing.T) {
	e := NewTextEdit()
	e.SetValue("hello")
	e.SelectAll()
	if e.SelectedText() != "hello" {
		t.Fatal(e.SelectedText())
	}
	e.ClearSelection()
	e.SetCursor(3, false)
	e.DeleteToLineEnd()
	if e.Value() != "hel" {
		t.Fatal(e.Value())
	}
	e.DeleteToLineStart()
	if e.Value() != "" {
		t.Fatal(e.Value())
	}
}

func TestTextEdit_InsertRunesFiltersControls(t *testing.T) {
	e := NewTextEdit()
	e.InsertRunes("a\nb\tc")
	if e.Value() != "abc" {
		t.Fatal(e.Value())
	}
}

func TestTextEdit_InsertRunesPreservesZWJEmoji(t *testing.T) {
	family := "👨‍👩‍👧" // 3 emoji joined by two ZWJ (U+200D, category Cf)
	e := NewTextEdit()
	e.InsertRunes(family)
	if e.Value() != family {
		t.Fatalf("ZWJ runes dropped: value=%q want %q", e.Value(), family)
	}
	if e.Len() != 5 {
		t.Fatalf("Len=%d want 5 runes (3 emoji + 2 ZWJ)", e.Len())
	}
}

func TestTextEdit_DeleteWordBack(t *testing.T) {
	e := NewTextEdit()
	e.SetValue("one two")
	e.MoveEnd(false)
	e.DeleteWordBack()
	if e.Value() != "one " && e.Value() != "one" {
		// after delete word "two", may leave trailing space depending on impl
		if e.Value() != "one " {
			t.Fatalf("%q", e.Value())
		}
	}
}
