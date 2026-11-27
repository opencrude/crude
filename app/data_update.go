package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"crude/agent/sessiondb"
	"crude/core"
	"crude/core/coreprotocol"
	"crude/internal/buildinfo"
	"crude/internal/dataformat"
	"crude/internal/fileuri"
	"crude/workspace/localhistory"
)

// dataStore is one kind of saved database and every place under the data
// folder where Crude keeps it, including deleted workspaces that can be
// restored. Each format owns its versions and steps; this list owns where the
// files are.
type dataStore struct {
	name     string // user words
	format   dataformat.Format
	patterns []string
	lock     func(path string) (func() error, error)
}

func dataStores() []dataStore {
	return []dataStore{
		{name: "Sessions and workspaces", format: sessiondb.Format, patterns: []string{
			"sessions/*.db", "sessions/*/subagents/*.db",
			"workspaces/*/workspace.db", "workspaces/*/sessions/*.db", "workspaces/*/sessions/*/subagents/*.db",
			"deleted-workspaces/*/data/workspace.db", "deleted-workspaces/*/data/sessions/*.db", "deleted-workspaces/*/data/sessions/*/subagents/*.db",
		}},
		{name: "Local History", format: localhistory.Format, lock: localhistory.LockDataUpdate, patterns: []string{
			"workspaces/*/local-history/history.db", "deleted-workspaces/*/data/local-history/history.db",
			"workspaces/*/execution-history/*/history.db", "deleted-workspaces/*/data/execution-history/*/history.db",
		}},
	}
}

// dataFile is one database the survey found.
type dataFile struct {
	store int
	path  string
	rel   string
	size  int64
	found dataformat.Inspection
	err   error
}

// steps reports whether the file needs update steps (and a backup first), not
// only its version stamp.
func (f dataFile) steps(stores []dataStore) bool {
	return f.found.Version >= 1 && f.found.Version < stores[f.store].format.Current
}

func openDataFile(path string, readOnly bool) (*sql.DB, error) {
	options := "_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	if readOnly {
		options = "mode=ro&" + options
	} else {
		options = "mode=rw&" + options
	}
	db, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, options))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// surveyData reads the format of every database under dataDir without
// changing any of them.
func surveyData(ctx context.Context, dataDir string, stores []dataStore) ([]dataFile, error) {
	var files []dataFile
	realRoot, err := filepath.EvalSymlinks(dataDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for index, store := range stores {
		for _, pattern := range store.patterns {
			matches, err := filepath.Glob(filepath.Join(dataDir, filepath.FromSlash(pattern)))
			if err != nil {
				return nil, err
			}
			for _, path := range matches {
				rel, err := filepath.Rel(dataDir, path)
				if err != nil {
					return nil, err
				}
				f := dataFile{store: index, path: path, rel: filepath.ToSlash(rel)}
				info, err := os.Lstat(path)
				if err != nil {
					f.err = err
				} else if !info.Mode().IsRegular() {
					f.err = errors.New("not a regular database file")
				} else if real, err := filepath.EvalSymlinks(path); err != nil || real != filepath.Join(realRoot, rel) {
					f.err = errors.New("the database path contains a link")
				} else {
					f.size = info.Size()
				}
				files = append(files, f)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].store != files[j].store {
			return files[i].store < files[j].store
		}
		return files[i].rel < files[j].rel
	})
	// Reading a header is fast but the folder can hold thousands of databases.
	work := make(chan int)
	var workers sync.WaitGroup
	for range runtime.NumCPU() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range work {
				f := &files[i]
				if f.err != nil {
					continue
				}
				db, err := openDataFile(f.path, true)
				if err == nil {
					f.found, err = stores[f.store].format.Inspect(ctx, db)
					if err == nil && f.found.Pending(stores[f.store].format) {
						err = db.QueryRowContext(ctx, `SELECT p.page_count * s.page_size FROM pragma_page_count p, pragma_page_size s`).Scan(&f.size)
					}
					err = errors.Join(err, db.Close())
				}
				f.err = err
			}
		}()
	}
	for i := range files {
		if ctx.Err() != nil {
			break
		}
		work <- i
	}
	close(work)
	workers.Wait()
	return files, ctx.Err()
}

// dataUpdatePlan is what one update run does.
type dataUpdatePlan struct {
	work        []dataFile // files to update, in order
	newer       []dataFile
	unreadable  []dataFile
	unsupported []dataFile
	backupBytes int64
	largest     int64
}

func planDataUpdate(files []dataFile, stores []dataStore) dataUpdatePlan {
	var plan dataUpdatePlan
	for _, f := range files {
		format := stores[f.store].format
		switch {
		case f.err != nil:
			plan.unreadable = append(plan.unreadable, f)
		case f.found.Version > format.Current:
			plan.newer = append(plan.newer, f)
		case f.found.Empty:
		case f.found.Version < 1:
			plan.unsupported = append(plan.unsupported, f)
		case f.found.Pending(format):
			plan.work = append(plan.work, f)
			plan.backupBytes += f.size
			plan.largest = max(plan.largest, f.size)
		}
	}
	return plan
}

// dataUpdateWeight is the progress weight of one file: its backup and its
// update for files with steps, a small constant for a version stamp.
func dataUpdateWeight(f dataFile, stores []dataStore) int64 {
	if f.steps(stores) {
		return 2*f.size + 1
	}
	return 4 << 10
}

func problemDetail(err error) string {
	text := strings.TrimSpace(err.Error())
	if line, _, found := strings.Cut(text, "\n"); found {
		text = line
	}
	if len(text) > 240 {
		text = text[:240] + "…"
	}
	return text
}

// dataUpdateRunner runs the data update of one core start.
type dataUpdateRunner struct {
	dataDir string
	stores  []dataStore
	server  *core.DataUpdateServer
	log     func(level, msg string, pairs ...any)
	theme   string
	// grace bounds how long a refusal waits for a shell that shows it.
	grace time.Duration
	// freeSpace reports the free bytes of the disk that holds a path.
	freeSpace func(ctx context.Context, path string) (int64, error)
	// show starts to answer shells with the update's progress. A core with
	// nothing to update never calls it, so its shells connect as usual.
	show func()
}

func (r *dataUpdateRunner) showUpdate() {
	if r.show != nil {
		r.show()
	}
}

func diskFreeSpace(ctx context.Context, path string) (int64, error) {
	usage, err := disk.UsageWithContext(ctx, path)
	if err != nil {
		return 0, err
	}
	return int64(usage.Free), nil
}

var errDataNewer = errors.New("saved data comes from a newer Crude")

// run updates every database that needs it and returns the final status. It
// returns errDataNewer, without changing anything, when a database comes from
// a newer Crude.
func (r *dataUpdateRunner) run(ctx context.Context) (coreprotocol.DataUpdate, error) {
	status := coreprotocol.DataUpdate{Phase: coreprotocol.DataUpdateChecking, Version: buildinfo.Version, Theme: r.theme}
	r.server.Publish(status, false)
	for {
		files, err := surveyData(ctx, r.dataDir, r.stores)
		if err != nil {
			return status, err
		}
		plan := planDataUpdate(files, r.stores)
		if len(plan.newer) > 0 {
			status.Phase, status.Final = coreprotocol.DataUpdateNewer, true
			status.Newer = nil
			for _, f := range plan.newer {
				r.log("WARN", "newer_data_file", "file", f.rel, "version", f.found.Version)
				if len(status.Newer) != 0 {
					continue
				}
				status.Newer = append(status.Newer, coreprotocol.DataUpdateProblem{Path: f.rel,
					Detail: fmt.Sprintf("format %d; this Crude reads format %d", f.found.Version, r.stores[f.store].format.Current)})
			}
			r.log("WARN", "newer_data", "files", len(plan.newer), "first", plan.newer[0].rel)
			r.server.Publish(status, false)
			r.showUpdate()
			r.waitForShells(ctx)
			return status, errDataNewer
		}
		if len(plan.unsupported)+len(plan.unreadable) > 0 {
			for _, f := range plan.unsupported {
				r.problem(&status, f, "Crude does not recognize this database format.")
			}
			for _, f := range plan.unreadable {
				r.problem(&status, f, problemDetail(f.err))
			}
			return r.block(ctx, status)
		}
		if plan.backupBytes > 0 {
			needed := plan.backupBytes + plan.largest
			free, err := r.freeSpace(ctx, r.dataDir)
			if err != nil {
				return status, fmt.Errorf("check space for the backup: %w", err)
			}
			if free < needed {
				status.Phase, status.Needed, status.Free = coreprotocol.DataUpdateNoSpace, needed, free
				r.log("WARN", "data_update_no_space", "needed", needed, "free", free)
				r.server.Publish(status, false)
				r.showUpdate()
				select {
				case <-ctx.Done():
					return status, ctx.Err()
				case <-r.server.Stopped():
					return status, context.Canceled
				case <-r.server.Retry():
					status.Needed, status.Free = 0, 0
					continue
				}
			}
		}
		return r.apply(ctx, status, plan)
	}
}

func (r *dataUpdateRunner) apply(ctx context.Context, status coreprotocol.DataUpdate, plan dataUpdatePlan) (coreprotocol.DataUpdate, error) {
	status.Phase, status.Files = coreprotocol.DataUpdateUpdating, len(plan.work)
	status.Stores = make([]coreprotocol.DataUpdateStore, len(r.stores))
	for i, store := range r.stores {
		status.Stores[i].Name = store.name
	}
	for _, f := range plan.work {
		status.Total += dataUpdateWeight(f, r.stores)
		status.Stores[f.store].Files++
	}
	backupDir := ""
	if plan.backupBytes > 0 {
		parent := filepath.Join(r.dataDir, "backups")
		if err := os.MkdirAll(parent, 0700); err != nil {
			return status, err
		}
		if info, err := os.Lstat(parent); err != nil || !info.IsDir() {
			return status, errors.New("the backup folder is not a directory")
		}
		var err error
		backupDir, err = os.MkdirTemp(parent, "update-"+time.Now().Format("20060102-150405")+"-")
		if err != nil {
			return status, err
		}
		status.Backup = backupDir
	}
	if len(plan.work) > 0 {
		r.log("INFO", "data_update_start", "files", len(plan.work), "bytes", plan.backupBytes, "backup", backupDir)
		r.server.Publish(status, true)
		r.showUpdate()
	}
	var published time.Time
	publish := func(force bool) {
		if force || time.Since(published) >= 50*time.Millisecond {
			published = time.Now()
			r.server.Publish(status, true)
		}
	}
	for _, f := range plan.work {
		if err := ctx.Err(); err != nil {
			return status, err
		}
		select {
		case <-r.server.Stopped():
			return status, context.Canceled
		default:
		}
		store := r.stores[f.store]
		status.Step = store.name
		base, weight := status.Done, dataUpdateWeight(f, r.stores)
		publish(true)
		err := r.updateFile(ctx, f, backupDir, func(done int64) {
			status.Done = base + min(done, weight)
			publish(false)
		})
		status.Done, status.FilesDone = base+weight, status.FilesDone+1
		if err != nil {
			if ctx.Err() != nil {
				return status, ctx.Err()
			}
			status.Stores[f.store].Failed++
			r.problem(&status, f, problemDetail(err))
			return r.block(ctx, status)
		} else {
			status.Stores[f.store].Done++
		}
		publish(true)
	}
	status.Phase, status.Step, status.Final = coreprotocol.DataUpdateFinished, "", true
	if len(plan.work) > 0 {
		r.log("INFO", "data_update_done", "files", len(plan.work), "problems", len(status.Problems))
	}
	r.server.Publish(status, false)
	return status, nil
}

// Send the first actionable path. Log every path locally, so the handshake
// never grows with the number of damaged databases.
func (r *dataUpdateRunner) problem(s *coreprotocol.DataUpdate, f dataFile, detail string) {
	r.log("WARN", "data_update_failed", "file", f.rel, "error", detail)
	if len(s.Problems) == 0 {
		s.Problems = []coreprotocol.DataUpdateProblem{{Path: f.rel, Detail: detail}}
	}
}
func (r *dataUpdateRunner) block(ctx context.Context, s coreprotocol.DataUpdate) (coreprotocol.DataUpdate, error) {
	s.Phase, s.Final = coreprotocol.DataUpdateBlocked, true
	r.server.Publish(s, false)
	r.showUpdate()
	r.waitForShells(ctx)
	return s, fmt.Errorf("data update stopped: %s: %s", s.Problems[0].Path, s.Problems[0].Detail)
}

// updateFile backs up one database when it needs steps, then updates it in
// one transaction. A failure leaves the database as it was.
func (r *dataUpdateRunner) updateFile(ctx context.Context, f dataFile, backupDir string, progress func(done int64)) (err error) {
	if lock := r.stores[f.store].lock; lock != nil {
		unlock, lockErr := lock(f.path)
		if lockErr != nil {
			return lockErr
		}
		defer func() { err = errors.Join(err, unlock()) }()
	}
	db, err := openDataFile(f.path, false)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = r.stores[f.store].format.Update(ctx, db, f.path, filepath.Join(backupDir, filepath.FromSlash(f.rel)), func(fraction float64) { progress(int64(fraction * float64(dataUpdateWeight(f, r.stores)))) })
	return err
}

// waitForShells keeps a refusal visible until no shell has shown it for the
// grace period, or until a stop.
func (r *dataUpdateRunner) waitForShells(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.server.Stopped():
			return
		case <-ticker.C:
			if r.server.IdleFor() >= r.grace {
				return
			}
		}
	}
}
