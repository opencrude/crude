package types

import "testing"

func TestLSPSourcePositionResolvesOnlyBorrowedUnicodeCRLFSource(t *testing.T) {
	const text = "package main\r\n// 😀 x := answer()\r\n"
	line, col, err := (LSPSourcePosition{Line: 2, Symbol: "answer"}).Resolve(text)
	if err != nil || line != 1 || col != 11 {
		t.Fatalf("source position=%d:%d %v", line, col, err)
	}
	for _, p := range []LSPSourcePosition{{Line: 0, Symbol: "answer"}, {Line: 9, Symbol: "answer"}, {Line: 2, Column: 100}, {Line: 2, Symbol: "absent"}, {Line: 2, Column: -1}, {Line: 2, Symbol: "\xff"}} {
		if _, _, err := p.Resolve(text); err == nil {
			t.Fatalf("invalid source intent accepted: %+v", p)
		}
	}
}
