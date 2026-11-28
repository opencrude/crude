package api

import (
	"math"
	"time"
)

// History lists past and far-future launches for the archive endpoint.
// Entries are sorted by NET. Keep the last one last.
var History = []Launch{
	{ID: "apo-11", Vehicle: "Saturn V", Site: "Stage 3, Borehamwood", NET: time.Date(1969, 7, 16, 13, 32, 0, 0, time.UTC)},
	{ID: "dsc-1", Vehicle: "Discovery One", Site: "Clavius", NET: time.Date(2001, 4, 2, 0, 0, 0, 0, time.UTC)},
	{ID: "gen-0", Vehicle: "Genesis", Site: "unknown", NET: time.Date(2009, 1, 3, 18, 15, 5, 0, time.UTC)},
	{ID: "ovl-237", Vehicle: "Overlook", Site: "LC-39A", NET: time.Unix(math.MaxInt32, 0).UTC()},
}
