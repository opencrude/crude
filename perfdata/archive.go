package perfdata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"crude/buildinfo"
	"crude/internal/fileuri"
	"crude/metrics"
	_ "modernc.org/sqlite"
)

const archiveSchema = `
CREATE TABLE IF NOT EXISTS performance_hours (
 role TEXT NOT NULL, build TEXT NOT NULL, cohort TEXT NOT NULL, incarnation TEXT NOT NULL,
 hour INTEGER NOT NULL, first INTEGER NOT NULL, until INTEGER NOT NULL,
 coverage INTEGER NOT NULL, sources TEXT NOT NULL, series TEXT NOT NULL,
 omitted INTEGER NOT NULL, overwritten INTEGER NOT NULL,
 PRIMARY KEY(incarnation,hour)
);
CREATE INDEX IF NOT EXISTS performance_hours_role_time ON performance_hours(role,hour DESC);
CREATE INDEX IF NOT EXISTS performance_hours_build_time ON performance_hours(role,build,hour DESC);
CREATE INDEX IF NOT EXISTS performance_hours_prune ON performance_hours(hour);
CREATE TABLE IF NOT EXISTS performance_stalls (
 id INTEGER PRIMARY KEY, role TEXT NOT NULL, build TEXT NOT NULL, cohort TEXT NOT NULL, incarnation TEXT NOT NULL,
 at INTEGER NOT NULL, evidence TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS performance_stalls_at ON performance_stalls(at);
CREATE INDEX IF NOT EXISTS performance_stalls_role_at ON performance_stalls(role,at DESC);
`

const archiveRetention = 90 * 24 * time.Hour
const stallRetention = 7 * 24 * time.Hour

func ArchivePath(home string) string {
	return ArchivePathIn(filepath.Join(home, buildinfo.DataDir()))
}

func ArchivePathIn(dataDir string) string { return filepath.Join(dataDir, "performance.db") }

type ArchiveSource struct {
	metrics.ArchiveProcess
	Role          string  `json:"role"`
	Build         string  `json:"build_id"`
	Cohort        string  `json:"source_cohort"`
	Version       string  `json:"version"`
	Commit        string  `json:"commit"`
	Platform      string  `json:"platform"`
	GoVersion     string  `json:"go_version"`
	Parallelism   int     `json:"gomaxprocs"`
	BucketUpperUS []int64 `json:"bucket_upper_us"`
}

// RecordArchive starts one host-local writer for the life of a shell or core.
// It consumes closed windows of the live rings; no observation or UI event
// waits for its SQLite commits. Close joins a bounded final flush.
func RecordArchive(path, role string, report func(error)) func() {
	started := time.Now().Unix()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		source := ArchiveSource{ArchiveProcess: metrics.ArchiveProcessIdentity(), Role: role,
			Build: buildinfo.ExecutableID(), Cohort: buildinfo.ProtocolBuild, Version: buildinfo.Version, Commit: buildinfo.Commit,
			Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Parallelism: runtime.GOMAXPROCS(0), BucketUpperUS: metrics.BucketUpperMicros()}
		var db *sql.DB
		defer func() {
			if db != nil {
				_ = db.Close()
			}
		}()
		next := started
		failed := false
		flush := func(now time.Time) {
			until := now.Unix()
			// Do not produce overlap on a backward clock jump and do not claim
			// a stalled recorder still covers seconds overwritten in the rings.
			next = max(next, until-metrics.HistorySeconds+1)
			if until <= next {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var err error
			if db == nil {
				db, err = openArchive(ctx, path)
			}
			for err == nil && next < until {
				end := min(until, (next/3600+1)*3600)
				window := metrics.CaptureArchiveWindow(next, end)
				err = storeArchiveWindow(ctx, db, source, window)
				if err == nil {
					next = end
				}
			}
			if err != nil && !failed && report != nil {
				report(err)
			}
			failed = err != nil
		}
		// Align cuts with UTC minutes. A final partial minute stays attributed
		// to the exact producer even when the shell lived less than a minute.
		for {
			at := time.Now()
			timer := time.NewTimer(time.Until(at.Truncate(time.Minute).Add(time.Minute).Add(time.Second)))
			select {
			case <-timer.C:
				flush(time.Now())
			case <-stop:
				timer.Stop()
				flush(time.Now())
				return
			}
		}
	}()
	return sync.OnceFunc(func() { close(stop); <-done })
}

func openArchive(ctx context.Context, path string) (*sql.DB, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, "mode=rw&_pragma=busy_timeout(750)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=journal_size_limit(4194304)"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA max_page_count=65536;`+archiveSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func storeArchiveWindow(ctx context.Context, db *sql.DB, source ArchiveSource, w metrics.ArchiveWindow) error {
	hour := w.From / 3600 * 3600
	if w.From >= w.Until || w.Until-hour > 3600 || source.Incarnation == "" {
		return errors.New("invalid performance observation window")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	clock := time.Unix(w.Until, 0)
	if _, err := tx.ExecContext(ctx, `DELETE FROM performance_hours WHERE hour<?;`, clock.Add(-archiveRetention).Unix()/3600*3600); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM performance_stalls WHERE at<?;`, clock.Add(-stallRetention).UnixNano()); err != nil {
		return err
	}
	var first, oldestUntil, coverage int64
	var existing []byte
	var omitted int
	var overwritten uint64
	err = tx.QueryRowContext(ctx, `SELECT first,until,coverage,series,omitted,overwritten FROM performance_hours WHERE incarnation=? AND hour=?`, source.Incarnation, hour).
		Scan(&first, &oldestUntil, &coverage, &existing, &omitted, &overwritten)
	series := make(map[string]metrics.Rollup)
	if err == nil {
		if w.Until <= oldestUntil {
			return nil
		} // original batch is already committed
		if w.From < oldestUntil {
			return errors.New("performance observation overlaps an already saved window")
		}
		if err := json.Unmarshal(existing, &series); err != nil {
			return err
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		first = w.From
	} else {
		return err
	}
	for name, next := range w.Series {
		row := series[name]
		if !row.Merge(next) {
			return fmt.Errorf("performance series %s changed inside one process", name)
		}
		series[name] = row
	}
	encoded, err := json.Marshal(series)
	if err != nil {
		return err
	}
	// Save thresholds with the producer. A later Crude build may change
	// its histogram without changing the number of buckets.
	if source.BucketUpperUS == nil {
		source.BucketUpperUS = metrics.BucketUpperMicros()
	}
	provenance, err := json.Marshal(source)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO performance_hours(role,build,cohort,incarnation,hour,first,until,coverage,sources,series,omitted,overwritten)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(incarnation,hour) DO UPDATE SET until=excluded.until,coverage=excluded.coverage,
	 series=excluded.series,omitted=excluded.omitted,overwritten=excluded.overwritten`, source.Role, source.Build, source.Cohort, source.Incarnation, hour, first, w.Until,
		coverage+w.Until-w.From, string(provenance), string(encoded), omitted+w.StallsOmitted, max(overwritten, w.EventRingOverwritten)); err != nil {
		return err
	}
	for _, stall := range w.Stalls {
		raw, err := json.Marshal(stall)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO performance_stalls(role,build,cohort,incarnation,at,evidence) VALUES(?,?,?,?,?,?)`,
			source.Role, source.Build, source.Cohort, source.Incarnation, stall.At.UnixNano(), string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
