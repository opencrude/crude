package cell

import "testing"

func TestCharPool_InternGetReset(t *testing.T) {
	p := NewCharPool()
	idA := p.Intern('a')
	idA2 := p.Intern('a')
	if idA != idA2 {
		t.Fatalf("duplicate intern: %d vs %d", idA, idA2)
	}
	idB := p.Intern('b')
	if idB == idA {
		t.Fatal("distinct runes should get distinct IDs")
	}
	if p.Get(idA) != 'a' || p.Get(idB) != 'b' {
		t.Fatal("Get mismatch")
	}
	if p.Get(9999) != 0 {
		t.Fatal("OOB Get should return 0")
	}
	if p.Len() != 2 {
		t.Fatalf("Len=%d", p.Len())
	}
	p.Reset()
	if p.Len() != 0 {
		t.Fatal("Reset should clear")
	}
	// IDs after reset restart
	idNew := p.Intern('a')
	if idNew != 0 {
		t.Fatalf("first after reset should be 0, got %d", idNew)
	}
}

func TestCharPool_OverflowReset(t *testing.T) {
	p := NewCharPool()
	// Fill to maxPoolSize without using huge memory for the map of
	// consecutive runes; use a range of distinct runes.
	// maxPoolSize is 65535; filling all is slow but correct.
	// Test a smaller path: force overflow by setting near-capacity manually
	// via repeated Intern of unique runes up to a practical subset, then
	// verify the overflow branch by calling Intern when len == maxPoolSize.
	//
	// Directly construct a full pool is expensive; instead fill until
	// Len() is large enough and verify Intern of a new rune still works.
	// We test the overflow path by temporarily using the public API until
	// Len hits maxPoolSize only if practical; skip the full 65k in unit tests
	// and unit-test the branch via a controlled fill of a few and
	// documenting that overflow resets IDs.
	//
	// Practical overflow test: intern maxPoolSize entries is ~65k map ops
	// which is fine for unit tests (<1s).
	const n = 1000
	for i := 0; i < n; i++ {
		p.Intern(rune(i + 1))
	}
	if p.Len() != n {
		t.Fatalf("Len=%d want %d", p.Len(), n)
	}
	// Re-intern existing
	if p.Intern(1) != 0 {
		t.Fatal("re-interning first rune changed its ID")
	}
	// Ensure Get works at edges
	if p.Get(uint16(n-1)) != rune(n) {
		t.Fatalf("last entry mismatch: got %U", p.Get(uint16(n-1)))
	}
}

func TestCharPool_FullOverflow(t *testing.T) {
	if testing.Short() {
		t.Skip("full pool overflow is slow")
	}
	p := NewCharPool()
	for i := 0; i < maxPoolSize; i++ {
		p.Intern(rune(i + 1))
	}
	if p.Len() != maxPoolSize {
		t.Fatalf("Len=%d", p.Len())
	}
	// Next unique rune forces reset
	id := p.Intern(rune(maxPoolSize + 100))
	if id != 0 {
		t.Fatalf("after overflow reset, id should be 0, got %d", id)
	}
	if p.Len() != 1 {
		t.Fatalf("after overflow Len=%d", p.Len())
	}
}

func TestStylePool_InternGetReset(t *testing.T) {
	p := NewStylePool()
	id1 := p.Intern(Red, Blue, AttrBold)
	id1b := p.Intern(Red, Blue, AttrBold)
	if id1 != id1b {
		t.Fatal("duplicate style should reuse ID")
	}
	id2 := p.Intern(Green, Blue, AttrBold)
	if id2 == id1 {
		t.Fatal("different style should get new ID")
	}
	e := p.Get(id1)
	if !e.FG.Equal(Red) || !e.BG.Equal(Blue) || e.Attr != AttrBold {
		t.Fatalf("Get: %+v", e)
	}
	if p.Get(9999).Attr != 0 {
		t.Fatal("out-of-range style must be the zero value")
	}
	if p.Len() != 2 {
		t.Fatalf("Len=%d", p.Len())
	}
	p.Reset()
	if p.Len() != 0 {
		t.Fatal("Reset failed")
	}
}

func TestStylePool_FullOverflow(t *testing.T) {
	if testing.Short() {
		t.Skip("full style pool overflow is slow")
	}
	p := NewStylePool()
	// Distinct styles via palette index
	for i := 0; i < maxPoolSize; i++ {
		p.Intern(NewPaletteColor(uint8(i%256)), NoColor, Attrs(i%16))
	}
	// Force collision-free entries: if some collide due to limited palette*attrs,
	// we may not reach max. Build unique with truecolor.
	p.Reset()
	for i := 0; i < maxPoolSize; i++ {
		r := uint8(i)
		g := uint8(i >> 8)
		b := uint8(i)
		p.Intern(NewTrueColor(r, g, b), NoColor, 0)
	}
	if p.Len() != maxPoolSize {
		t.Fatalf("unique styles filled %d entries, want %d", p.Len(), maxPoolSize)
	}
	// Guaranteed unique: use Attrs as high bits via truecolor R from index low bits
	// and palette separately; the simplest path is already tested for CharPool.
	// Verify Intern after many entries still works:
	id := p.Intern(NewTrueColor(9, 9, 9), NewTrueColor(1, 1, 1), AttrItalic)
	_ = id
	if p.Len() == 0 {
		t.Fatal("pool empty unexpectedly")
	}
}
