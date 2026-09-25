package codexio

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"
)

func TestNormalizedModeLiveParity(t *testing.T) {
	bin := codexBinary(t)
	g := newGenerator(738193)
	for i := range 300 {
		c := g.patch(fmt.Sprintf("normalized-%d", i))
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		want, err := runOracleMode(ctx, bin, c.Files, c.Patch, NormalizeToLf)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		got, applyErr := NormalizeToLf.ApplyFiles(c.Files, c.Patch)
		if (applyErr == nil) != want.ok || !maps.Equal(got, want.files) {
			t.Fatalf("%s\nwant %q (ok=%v, %s)\ngot %q (%v)", c, want.files, want.ok, want.stderr, got, applyErr)
		}
	}
}
