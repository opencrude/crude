package editor

import "testing"

func TestInvalidateWidthProfileDropsGeometryOnly(t *testing.T) {
	ed := &Editor{
		lineRunesCacheValid: true,
		lineRunesCacheLine:  4,
		lineRunesCache:      []rune("☰ text"),
		lineGeomCache:       map[int]*lineGeomCache{4: {raw: "☰ text", totalW: 6}},
	}
	ed.InvalidateWidthProfile()
	if ed.lineRunesCacheValid || ed.lineRunesCache != nil || ed.lineGeomCache != nil {
		t.Fatalf("width geometry survived invalidation: %+v", ed.lineGeomCache)
	}
}
