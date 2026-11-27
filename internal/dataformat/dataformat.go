// Package dataformat versions Crude's saved SQLite databases and updates a
// database saved by an older Crude to the current format.
//
// The design follows Chromium's sql::MetaTable and Signal Desktop's schema
// updates, both used on very large installed bases:
//
//   - Each database records its format version in SQLite's user_version header
//     field. The version changes in the same transaction as the data it
//     describes, so a crash leaves the old data with the old version or the
//     new data with the new version, never a mix.
//   - Ordered steps take a database from one version to the next. A database
//     of any older supported version runs every later step in order.
//   - A database from a newer Crude is refused, never changed.
//   - Steps are frozen. They use only SQL and the shapes that existed when they
//     were written, never current product code, so they keep working however
//     the product changes later.
//
// Product code opens only current databases (Require). Every step runs from
// the data update, before the core opens any database.
package dataformat

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"crude/internal/fileuri"
	"modernc.org/sqlite"
)

// Querier is the SQL connection a step works on: one connection inside the
// update's write transaction.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Format describes one kind of database, for example a session database.
type Format struct {
	// Name is the user word for these databases, for example "sessions".
	Name string
	// Current is the version this Crude reads and writes.
	Current int
	// Classify names the version of a database saved before formats had
	// versions (user_version 0). It returns 0 for a shape that no step can
	// update; such a database is left as it is.
	Classify func(ctx context.Context, q Querier) (int, error)
	// Steps take a database to the version in their To field, in order. Step
	// To values are 2, 3, ... up to Current: version 1 is the oldest shape
	// that Classify recognizes.
	Steps []Step
}

// Step updates a database to one version.
type Step struct {
	To int
	// Title says what the step does, in user words.
	Title string
	Apply func(ctx context.Context, u Unit) error
}

// Unit is the database a step updates.
type Unit struct {
	Q Querier
	// Path is the database file, for steps that verify files beside it.
	Path string
	// Progress reports the finished fraction of this step, from 0 to 1.
	Progress func(fraction float64)
}

// ErrNewer reports a database saved by a newer Crude.
type ErrNewer struct {
	Format  string
	Found   int
	Current int
}

func (e *ErrNewer) Error() string {
	return fmt.Sprintf("this %s data was saved by a newer Crude (format %d; this Crude reads format %d)", e.Format, e.Found, e.Current)
}

// ErrNeedsUpdate reports a database that the data update did not update.
type ErrNeedsUpdate struct {
	Format  string
	Found   int
	Current int
}

func (e *ErrNeedsUpdate) Error() string {
	if e.Found == 0 {
		return fmt.Sprintf("this %s data was saved by an older Crude and was not updated; restart Crude to update it", e.Format)
	}
	return fmt.Sprintf("this %s data has format %d and was not updated to format %d; restart Crude to update it", e.Format, e.Found, e.Current)
}

// ErrUnsupported reports a shape older than every step: no update applies.
var ErrUnsupported = errors.New("saved by a Crude too old to update")

// Validate checks that the steps form one unbroken chain to Current.
func (f Format) Validate() error {
	if f.Name == "" || f.Current < 1 || f.Classify == nil {
		return errors.New("dataformat: incomplete format")
	}
	if len(f.Steps) != f.Current-1 {
		return fmt.Errorf("dataformat: %s has %d steps for %d versions", f.Name, len(f.Steps), f.Current)
	}
	for i, step := range f.Steps {
		if step.To != i+2 || step.Apply == nil || step.Title == "" {
			return fmt.Errorf("dataformat: %s step %d is not the step to version %d", f.Name, i+1, i+2)
		}
	}
	return nil
}

// Version reads the stored format version (0 before formats had versions).
func Version(ctx context.Context, q Querier) (int, error) {
	var version int
	err := q.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version)
	return version, err
}

// Empty reports a database without tables.
func Empty(ctx context.Context, q Querier) (bool, error) {
	var tables bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%')`).Scan(&tables)
	return !tables, err
}

// Stamp records the current version. Call it inside the transaction that
// creates the database's tables.
func (f Format) Stamp(ctx context.Context, q Querier) error {
	_, err := q.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, f.Current))
	return err
}

// Require reports whether product code may use the database: it must have the
// current version. An empty database returns fresh=true; the caller creates
// its tables and calls Stamp in the same transaction.
func (f Format) Require(ctx context.Context, q Querier) (fresh bool, err error) {
	version, err := Version(ctx, q)
	if err != nil {
		return false, err
	}
	switch {
	case version == f.Current:
		return false, nil
	case version > f.Current:
		return false, &ErrNewer{Format: f.Name, Found: version, Current: f.Current}
	case version == 0:
		empty, err := Empty(ctx, q)
		if err != nil || empty {
			return empty, err
		}
	}
	return false, &ErrNeedsUpdate{Format: f.Name, Found: version, Current: f.Current}
}

// Inspection is what Inspect found about one database.
type Inspection struct {
	// Version is the stored version, or the classified version of an
	// unversioned database. 0 means unsupported.
	Version int
	// Stamped reports a stored version.
	Stamped bool
	// Empty reports a database without tables.
	Empty bool
}

// Pending reports whether Update has work: steps to run or a version to stamp.
func (i Inspection) Pending(f Format) bool {
	return !i.Empty && i.Version >= 1 && (i.Version < f.Current || !i.Stamped)
}

// Inspect reads the version of a database without changing it.
func (f Format) Inspect(ctx context.Context, q Querier) (Inspection, error) {
	version, err := Version(ctx, q)
	if err != nil {
		return Inspection{}, err
	}
	if version != 0 {
		return Inspection{Version: version, Stamped: true}, nil
	}
	empty, err := Empty(ctx, q)
	if err != nil || empty {
		return Inspection{Empty: empty}, err
	}
	version, err = f.Classify(ctx, q)
	return Inspection{Version: version}, err
}

// Update takes an open database to the current version in one write
// transaction on one connection. The progress callback receives the finished
// fraction of the whole update. It returns the version the database had.
func (f Format) Update(ctx context.Context, db *sql.DB, path, dest string, progress func(float64)) (int, error) {
	if err := f.Validate(); err != nil {
		return 0, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	// IMMEDIATE takes the write lock now: no other writer can change the
	// database between the version read and the commit.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	found, err := f.Inspect(ctx, conn)
	if err != nil {
		return 0, err
	}
	switch {
	case found.Empty:
		return 0, nil
	case found.Version < 1:
		return 0, ErrUnsupported
	case found.Version > f.Current:
		return found.Version, &ErrNewer{Format: f.Name, Found: found.Version, Current: f.Current}
	}
	if !found.Pending(f) {
		return found.Version, nil
	}
	// Hold the reserved writer lock across snapshot creation AND conversion.
	// SQLite's backup source cannot be a connection with an open write
	// transaction, so a second, read-only handle reads that locked preimage.
	source, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, "mode=ro"))
	if err != nil {
		return found.Version, err
	}
	err = errors.Join(backup(ctx, source, dest, func(fraction float64) {
		if progress != nil {
			progress(fraction / 2)
		}
	}), source.Close())
	if err != nil {
		return found.Version, fmt.Errorf("back up: %w", err)
	}
	steps := f.Steps[found.Version-1:]
	for i, step := range steps {
		report := func(fraction float64) {
			if progress != nil {
				progress(0.5 + 0.5*(float64(i)+min(max(fraction, 0), 1))/float64(len(steps)))
			}
		}
		if err := step.Apply(ctx, Unit{Q: conn, Path: path, Progress: report}); err != nil {
			return found.Version, fmt.Errorf("%s: %w", step.Title, err)
		}
		report(1)
	}
	if err := f.Stamp(ctx, conn); err != nil {
		return found.Version, err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return found.Version, err
	}
	committed = true
	if progress != nil {
		progress(1)
	}
	return found.Version, nil
}

// backup uses SQLite's online backup API. It copies the committed view, not
// a database file that may exclude WAL pages. Never overwrite another backup.
func backup(ctx context.Context, db *sql.DB, dest string, progress func(float64)) (err error) {
	if dest == "" {
		return errors.New("missing database backup destination")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dest)
		}
	}()
	if err = out.Close(); err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var pages int64
	if err := conn.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return err
	}
	err = conn.Raw(func(driver any) (err error) {
		source, ok := driver.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("database driver does not support SQLite backup")
		}
		b, err := source.NewBackup(fileuri.SQLiteDSN(dest, "mode=rw&_pragma=synchronous(FULL)"))
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, b.Finish()) }()
		// Step in pages so cancellation does not wait for an entire large store.
		const batch = 128
		for copied := int64(0); ; {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := b.Step(batch)
			if err != nil {
				return err
			}
			if !more {
				break
			}
			copied += batch
			if progress != nil && pages > 0 {
				progress(min(1, float64(copied)/float64(pages)))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	verify, err := sql.Open("sqlite", fileuri.SQLiteDSN(dest, "mode=ro"))
	if err != nil {
		return err
	}
	defer verify.Close()
	var check string
	if err := verify.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("backup did not pass the database check: %s", check)
	}
	if progress != nil {
		progress(1)
	}
	return nil
}

// ClassifyTables returns the released layout matching all of a store's own
// table columns. tableNames is a fixed SQL list supplied by the format, never
// a filename or user input. Workspace and session tables may share one DB.
func ClassifyTables(ctx context.Context, q Querier, tableNames string, layouts map[string]int) (int, error) {
	rows, err := q.QueryContext(ctx, `SELECT json_array(m.name,p.name,upper(p.type),p."notnull",p.dflt_value,p.pk) FROM sqlite_schema m, pragma_table_info(m.name) p WHERE m.type='table' AND m.name IN (`+tableNames+`) ORDER BY m.name,p.name`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return 0, err
		}
		fmt.Fprintln(h, line)
	}
	return layouts[hex.EncodeToString(h.Sum(nil))], rows.Err()
}
