package perfdata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"time"

	"crude/internal/fileuri"
	"crude/internal/metrics"
)

// History is one process role's saved performance over recent hours: every
// build seen, each build's merged measurements and the newest stalls.
type History struct {
	Role   string
	Hours  int
	Builds []HistoryBuild // newest first
	Stalls []HistoryStall // newest first
	// StallsTotal counts every saved stall in the period; Stalls holds the
	// newest of them.
	StallsTotal       int
	StallsOmitted     int
	EventsOverwritten bool
	Partial           bool
	// Saved is false when this host has no performance history yet.
	Saved bool
}

// HistoryBuild is one build's saved observation in the period.
type HistoryBuild struct {
	Build, Version, Commit, Platform, GoVersion string
	First, Last                                 time.Time
	Observed                                    time.Duration // process time covered, summed over processes
	Processes                                   int
	// Series merges the build's hourly rows. Latency percentiles use Bounds.
	Series map[string]metrics.Rollup
	Bounds []int64
}

// HistoryStall is one saved stall.
type HistoryStall struct {
	metrics.ArchiveStall
	Build string
}

// Percentile reads one merged latency series of the build.
func (b HistoryBuild) Percentile(name string, fraction float64) (int64, bool) {
	r, ok := b.Series[name]
	if !ok || r.Kind != metrics.KindLatency || r.Count == 0 {
		return 0, false
	}
	return r.Percentile(b.Bounds, fraction), true
}

const historyStalls = 40

// ReadHistory reads the saved performance of role over the last hours from
// the archive at path. It only reads: a busy or missing archive never blocks
// a writer, and a missing archive is an empty, unsaved history.
func ReadHistory(ctx context.Context, path, role string, hours int) (History, error) {
	h := History{Role: role, Hours: hours}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return h, nil
	} else if err != nil {
		return h, err
	}
	db, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(750)"))
	if err != nil {
		return h, err
	}
	defer func() { _ = db.Close() }()
	h.Saved = true
	from := time.Now().UTC().Truncate(time.Hour).Add(-time.Duration(max(1, hours)-1) * time.Hour).Unix()
	rows, err := db.QueryContext(ctx, `SELECT build,sources,first,until,coverage,incarnation,series,omitted,overwritten FROM performance_hours
	 WHERE role=? AND hour>=? ORDER BY until DESC LIMIT 4097`, role, from)
	if err != nil {
		return h, err
	}
	builds := map[string]*HistoryBuild{}
	incarnations := map[string]map[string]bool{}
	count := 0
	for rows.Next() {
		if count++; count > 4096 {
			h.Partial = true
			break // bounded: older rows of a very busy host are left out
		}
		var build, sources, incarnation, series string
		var first, until, coverage int64
		var omitted int
		var overwritten uint64
		if err := rows.Scan(&build, &sources, &first, &until, &coverage, &incarnation, &series, &omitted, &overwritten); err != nil {
			_ = rows.Close()
			return h, err
		}
		h.StallsOmitted += omitted
		h.EventsOverwritten = h.EventsOverwritten || overwritten > 0
		var source ArchiveSource
		var rollups map[string]metrics.Rollup
		if json.Unmarshal([]byte(sources), &source) != nil || json.Unmarshal([]byte(series), &rollups) != nil {
			h.Partial = true
			continue // a damaged row is not evidence
		}
		b := builds[build]
		if b == nil {
			b = &HistoryBuild{Build: build, Version: source.Version, Commit: source.Commit, Platform: source.Platform,
				GoVersion: source.GoVersion, Series: map[string]metrics.Rollup{}, Bounds: source.BucketUpperUS}
			builds[build], incarnations[build] = b, map[string]bool{}
		}
		if b.First.IsZero() || time.Unix(first, 0).Before(b.First) {
			b.First = time.Unix(first, 0)
		}
		if time.Unix(until, 0).After(b.Last) {
			b.Last = time.Unix(until, 0)
		}
		b.Observed += time.Duration(coverage) * time.Second
		incarnations[build][incarnation] = true
		// Histograms merge only with the same bucket bounds.
		if !slices.Equal(source.BucketUpperUS, b.Bounds) {
			h.Partial = true
			continue
		}
		for name, rollup := range rollups {
			merged := b.Series[name]
			if merged.Merge(rollup) {
				b.Series[name] = merged
			} else {
				h.Partial = true
			}
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return h, err
	}
	for name, b := range builds {
		b.Processes = len(incarnations[name])
		h.Builds = append(h.Builds, *b)
	}
	slices.SortFunc(h.Builds, func(a, b HistoryBuild) int { return b.Last.Compare(a.Last) })

	fromNano := time.Unix(from, 0).UnixNano()
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_stalls WHERE role=? AND at>=?`, role, fromNano).Scan(&h.StallsTotal); err != nil {
		return h, err
	}
	stalls, err := db.QueryContext(ctx, `SELECT build,evidence FROM performance_stalls WHERE role=? AND at>=? ORDER BY at DESC,id DESC LIMIT ?`,
		role, fromNano, historyStalls)
	if err != nil {
		return h, err
	}
	defer stalls.Close()
	for stalls.Next() {
		var stall HistoryStall
		var evidence string
		if err := stalls.Scan(&stall.Build, &evidence); err != nil {
			return h, err
		}
		if json.Unmarshal([]byte(evidence), &stall.ArchiveStall) == nil {
			h.Stalls = append(h.Stalls, stall)
		} else {
			h.Partial = true
		}
	}
	return h, stalls.Err()
}
