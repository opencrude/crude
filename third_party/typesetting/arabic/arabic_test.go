// SPDX-License-Identifier: Unlicense OR BSD-3-Clause

package arabic

import "testing"

func shape(s string) string {
	text := []rune(s)
	forms := make([]Form, len(text))
	Join(text, forms)
	out := make([]rune, len(text))
	for i, r := range text {
		out[i] = r
		if p, ok := PresentationForm(r, forms[i]); ok {
			out[i] = p
		}
	}
	return string(out)
}

func TestJoinPersianWords(t *testing.T) {
	cases := map[string]string{
		// sin lam alef mim: initial, medial, final, isolated
		"سلام": "\ufeb3\ufee0\ufe8e\ufee1",
		// ZWNJ stops joining: mim yeh | khah waw alef heh mim
		"می\u200cخواهم": "\ufee3\ufbfd\u200c\ufea7\ufeee\ufe8d\ufeeb\ufee2",
		// Persian letters from Presentation Forms-A: peh, gaf, keheh
		"پگاه": "\ufb58\ufb95\ufe8e\ufee9",
		"کتاب": "\ufb90\ufe98\ufe8e\ufe8f",
	}
	for in, want := range cases {
		if got := shape(in); got != want {
			t.Errorf("shape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarksAreTransparent(t *testing.T) {
	// beh + kasra + teh: the kasra does not break the join.
	text := []rune("بِت")
	forms := make([]Form, len(text))
	Join(text, forms)
	if forms[0] != Initial || forms[1] != None || forms[2] != Final {
		t.Fatalf("forms = %v, want [Initial None Final]", forms)
	}
}

func TestNominalInvertsPresentationForm(t *testing.T) {
	for r := rune(firstArabicShape); r <= lastArabicShape; r++ {
		for _, f := range []Form{Isolated, Final, Medial, Initial} {
			p, ok := PresentationForm(r, f)
			if !ok {
				continue
			}
			if back, ok := Nominal(p); !ok || back != r {
				t.Fatalf("Nominal(%U) = %U, %v; want %U", p, back, ok, r)
			}
		}
	}
	if _, ok := Nominal('a'); ok {
		t.Fatal("Nominal accepted a non-presentation character")
	}
}

func TestJoins(t *testing.T) {
	if !Joins('ب') || Joins('a') || Joins('\u064e') || Joins('\u200c') {
		t.Fatal("Joins misclassified a character")
	}
}
