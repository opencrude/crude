package perfdata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"crude/internal/fileuri"
	"crude/internal/metrics"
	"crude/types/inspect"
)

type ArchiveQuery struct {
	Page    inspect.Request `json:"page"`
	Build   string          `json:"build_id,omitempty"`
	Against string          `json:"against_build_id,omitempty"`
	Hours   int             `json:"hours,omitempty"`
}

type archiveReadSource struct {
	Role      string `json:"role"`
	Build     string `json:"build_id,omitempty"`
	Against   string `json:"against_build_id,omitempty"`
	Hours     int    `json:"hours"`
	FromHour  int64  `json:"from_unix_hour"`
	Available bool   `json:"available"`
	Omitted   bool   `json:"older_matching_rows_omitted"`
}

type archiveHour struct {
	Source      ArchiveSource       `json:"source"`
	Hour        int64               `json:"unix_hour"`
	First       int64               `json:"first_unix_second"`
	Until       int64               `json:"until_unix_second"`
	Coverage    int64               `json:"observed_process_seconds"`
	Omitted     int                 `json:"stalls_omitted"`
	Overwritten uint64              `json:"ring_overwritten_lifetime"`
	Measurement *archiveMeasurement `json:"measurement,omitempty"`
}

type archiveMeasurement struct {
	metrics.Rollup
	P50 int64 `json:"p50,omitempty"`
	P95 int64 `json:"p95,omitempty"`
	P99 int64 `json:"p99,omitempty"`
}

func archiveMeasure(r metrics.Rollup, bounds []int64) archiveMeasurement {
	return archiveMeasurement{Rollup: r, P50: r.Percentile(bounds, .5), P95: r.Percentile(bounds, .95), P99: r.Percentile(bounds, .99)}
}

func InspectArchive(ctx context.Context, store *inspect.Store, owner, path, role string, in ArchiveQuery) (inspect.Page, error) {
	r := in.Page
	if role != "shell" && role != "core" {
		return inspect.Page{}, errors.New("performance archive owner is unavailable")
	}
	if in.Hours < 0 || in.Hours > 2160 || len(in.Build) > 64 || len(in.Against) > 64 || strings.ContainsAny(in.Build+in.Against, "\x00\r\n") {
		return inspect.Page{}, errors.New("use a retained build ID and a range of 1 to 2160 hours")
	}
	if r.SnapshotID == "" {
		if r.View == "" {
			r.View = "builds"
		}
		switch r.View {
		case "builds", "stalls", "hours":
			if in.Against != "" {
				return inspect.Page{}, errors.New("use the compare view to select a second build")
			}
		case "compare":
			if r.Filter == "" || in.Build == "" || in.Against == "" || in.Build == in.Against {
				return inspect.Page{}, errors.New("give two different build IDs and the exact metric name in page.filter")
			}
		default:
			return inspect.Page{}, errors.New("use builds, hours, stalls or compare")
		}
	}
	page, err := store.Read(ctx, owner, r, func(ctx context.Context) (inspect.Observation, error) {
		hours := in.Hours
		if hours == 0 {
			if r.View == "builds" || r.View == "compare" {
				hours = 2160
			} else {
				hours = 24
			}
		}
		from := time.Now().UTC().Truncate(time.Hour).Add(-time.Duration(hours-1) * time.Hour).Unix()
		src := archiveReadSource{Role: role, Build: in.Build, Against: in.Against, Hours: hours, FromHour: from}
		out := inspect.Observation{Notes: []string{"Local observed use, not a controlled benchmark. Coverage counts captured process seconds, which can overlap across processes. A gap or absent series does not mean zero stalls.", "Latency counts are observations. Gauge counts are seconds with a reading. Counter sums count events; their maximum is a one-second peak."}}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			out.Source, _ = json.Marshal(src)
			out.Notes = append(out.Notes, "No performance history has been saved on this host yet.")
			return out, nil
		} else if err != nil {
			return out, err
		}
		db, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(750)"))
		if err != nil {
			return out, err
		}
		defer func() { _ = db.Close() }()
		src.Available = true
		switch r.View {
		case "builds":
			out.Records, src.Omitted, err = archiveBuilds(ctx, db, src, r.Filter)
		case "stalls":
			out.Records, src.Omitted, err = archiveStalls(ctx, db, src, r.Filter)
		case "hours":
			out.Records, src.Omitted, err = archiveHours(ctx, db, src, r.Filter)
		case "compare":
			out.Records, err = archiveComparison(ctx, db, src, r.Filter)
		}
		if err != nil {
			return out, err
		}
		out.Source, _ = json.Marshal(src)
		return out, nil
	})
	if err != nil {
		return page, err
	}
	var src archiveReadSource
	if err := json.Unmarshal(page.Source, &src); err != nil {
		return inspect.Page{}, err
	}
	if (in.Build != "" && in.Build != src.Build) || (in.Against != "" && in.Against != src.Against) || (in.Hours != 0 && in.Hours != src.Hours) {
		return inspect.Page{}, errors.New("the archive selection changed. Start a new read without the snapshot ID")
	}
	return page, nil
}

const archivePageRows = 128

func packedArchiveRecord(kind, id string, value any) inspect.Record {
	raw, _ := json.Marshal(value)
	return inspect.Record{Kind: kind, ID: id, Value: raw}
}

func archiveBuilds(ctx context.Context, db *sql.DB, src archiveReadSource, filter string) ([]inspect.Record, bool, error) {
	rows, err := db.QueryContext(ctx, `WITH builds AS (
	 SELECT build,cohort, MIN(first) first,MAX(until) last,SUM(coverage) coverage,COUNT(DISTINCT incarnation) processes,
	 json_extract(sources,'$.version') version,json_extract(sources,'$.commit') revision,
	 json_extract(sources,'$.platform') platform,json_extract(sources,'$.go_version') go_version
	 FROM performance_hours WHERE role=? AND hour>=? AND (?='' OR build=?) AND (?='' OR instr(build,?)>0 OR instr(cohort,?)>0 OR instr(json_extract(sources,'$.version'),?)>0)
	 GROUP BY build,cohort,version,revision,platform,go_version)
	 SELECT build,cohort,first,last,coverage,processes,version,revision,platform,go_version FROM builds ORDER BY last DESC,build LIMIT ?`,
		src.Role, src.FromHour, src.Build, src.Build, filter, filter, filter, filter, archivePageRows+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var records []inspect.Record
	for rows.Next() {
		if len(records) == archivePageRows {
			return records, true, nil
		}
		var b struct {
			Build     string `json:"build_id"`
			Cohort    string `json:"source_cohort"`
			First     int64  `json:"first_unix_second"`
			Last      int64  `json:"until_unix_second"`
			Coverage  int64  `json:"observed_process_seconds"`
			Processes int    `json:"process_incarnations"`
			Version   string `json:"version"`
			Commit    string `json:"commit"`
			Platform  string `json:"platform"`
			Go        string `json:"go_version"`
		}
		if err := rows.Scan(&b.Build, &b.Cohort, &b.First, &b.Last, &b.Coverage, &b.Processes, &b.Version, &b.Commit, &b.Platform, &b.Go); err != nil {
			return nil, false, err
		}
		records = append(records, packedArchiveRecord("build", b.Build, b))
	}
	return records, false, rows.Err()
}

func archiveStalls(ctx context.Context, db *sql.DB, src archiveReadSource, filter string) ([]inspect.Record, bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,build,cohort,incarnation,evidence FROM performance_stalls
	 WHERE role=? AND at>=? AND (?='' OR build=?) AND (?='' OR json_extract(evidence,'$.series')=? OR json_extract(evidence,'$.label')=? OR EXISTS(SELECT 1 FROM json_each(evidence,'$.stack') f WHERE instr(f.value,?)>0))
	 ORDER BY at DESC,id DESC LIMIT ?`, src.Role, time.Unix(src.FromHour, 0).UnixNano(), src.Build, src.Build, filter, filter, filter, filter, archivePageRows+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var records []inspect.Record
	for rows.Next() {
		if len(records) == archivePageRows {
			return records, true, nil
		}
		var id uint64
		var raw json.RawMessage
		var value struct {
			Build       string          `json:"build_id"`
			Cohort      string          `json:"source_cohort"`
			Incarnation string          `json:"incarnation"`
			Stall       json.RawMessage `json:"stall"`
		}
		var text string
		if err := rows.Scan(&id, &value.Build, &value.Cohort, &value.Incarnation, &text); err != nil {
			return nil, false, err
		}
		raw = json.RawMessage(text)
		if !json.Valid(raw) {
			return nil, false, errors.New("a saved stall could not be read")
		}
		value.Stall = raw
		records = append(records, packedArchiveRecord("stall", fmt.Sprint(id), value))
	}
	return records, false, rows.Err()
}

func archiveHours(ctx context.Context, db *sql.DB, src archiveReadSource, filter string) ([]inspect.Record, bool, error) {
	// Match the JSON member by its literal key, not by caller-supplied JSONPath.
	rows, err := db.QueryContext(ctx, `SELECT sources,hour,first,until,coverage,omitted,overwritten,
	 (SELECT j.value FROM json_each(series) j WHERE j.key=?) FROM performance_hours
	 WHERE role=? AND hour>=? AND (?='' OR build=?) ORDER BY hour DESC,until DESC,incarnation LIMIT ?`,
		filter, src.Role, src.FromHour, src.Build, src.Build, archivePageRows+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var records []inspect.Record
	for rows.Next() {
		if len(records) == archivePageRows {
			return records, true, nil
		}
		var row archiveHour
		var source string
		var series sql.NullString
		if err := rows.Scan(&source, &row.Hour, &row.First, &row.Until, &row.Coverage, &row.Omitted, &row.Overwritten, &series); err != nil {
			return nil, false, err
		}
		if err := json.Unmarshal([]byte(source), &row.Source); err != nil {
			return nil, false, err
		}
		if series.Valid {
			var rollup metrics.Rollup
			if err := json.Unmarshal([]byte(series.String), &rollup); err != nil {
				return nil, false, err
			}
			m := archiveMeasure(rollup, row.Source.BucketUpperUS)
			row.Measurement = &m
		}
		records = append(records, packedArchiveRecord("process_hour", fmt.Sprintf("%s/%d", row.Source.Incarnation, row.Hour), row))
	}
	return records, false, rows.Err()
}

func archiveComparison(ctx context.Context, db *sql.DB, src archiveReadSource, filter string) ([]inspect.Record, error) {
	var out []inspect.Record
	for _, build := range []string{src.Build, src.Against} {
		rows, err := db.QueryContext(ctx, `SELECT first,until,coverage,json_extract(sources,'$.bucket_upper_us'),(SELECT j.value FROM json_each(series) j WHERE j.key=?)
		 FROM performance_hours WHERE role=? AND build=? AND hour>=? ORDER BY hour DESC LIMIT 8193`, filter, src.Role, build, src.FromHour)
		if err != nil {
			return nil, err
		}
		var aggregate metrics.Rollup
		var bounds []int64
		var first, last, coverage, withMetric int64
		count := 0
		for rows.Next() {
			count++
			if count > 8192 {
				_ = rows.Close()
				return nil, errors.New("too many saved observations. Reduce the comparison period")
			}
			var from, until, n int64
			var series sql.NullString
			var producerBounds string
			if err := rows.Scan(&from, &until, &n, &producerBounds, &series); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if first == 0 || from < first {
				first = from
			}
			last = max(last, until)
			coverage += n
			if series.Valid {
				var rollup metrics.Rollup
				if err := json.Unmarshal([]byte(series.String), &rollup); err != nil {
					_ = rows.Close()
					return nil, err
				}
				if rollup.Kind == metrics.KindLatency {
					var binEnds []int64
					if err := json.Unmarshal([]byte(producerBounds), &binEnds); err != nil {
						_ = rows.Close()
						return nil, err
					}
					if len(binEnds)+1 != len(rollup.Buckets) || (bounds != nil && !slices.Equal(bounds, binEnds)) {
						_ = rows.Close()
						return nil, errors.New("saved histogram formats do not match within this build")
					}
					bounds = binEnds
				}
				if !aggregate.Merge(rollup) {
					_ = rows.Close()
					return nil, errors.New("saved metric formats do not match")
				}
				withMetric += n
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		value := map[string]any{"build_id": build, "metric": filter, "process_hours": count, "first_unix_second": first, "until_unix_second": last, "observed_process_seconds": coverage, "process_seconds_in_windows_with_metric": withMetric, "metric_observed": aggregate.Count > 0}
		if aggregate.Count > 0 {
			value["measurement"] = archiveMeasure(aggregate, bounds)
			if bounds != nil {
				value["bucket_upper_us"] = bounds
			}
		}
		out = append(out, packedArchiveRecord("build_observation", build, value))
	}
	return out, nil
}
