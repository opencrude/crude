package workspace

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"crude/internal/fileuri"
)

// UsageReport is the model use recorded in a workspace's session databases,
// sub-agent databases included. Each database holds only the requests of its
// own agents, so the sums never count a request twice. A cost that the
// provider or price table did not report is counted in UnknownCost and never
// estimated here.
type UsageReport struct {
	GeneratedAt int64         `json:"generated_unix_ns"`
	Requests    int           `json:"requests"`
	CostUSD     float64       `json:"cost_usd"`
	UnknownCost int           `json:"unknown_cost,omitempty"`
	First       int64         `json:"first_unix_ns,omitempty"`
	Last        int64         `json:"last_unix_ns,omitempty"`
	Models      []ModelCost   `json:"models"`
	Days        []DayCost     `json:"days"`
	Sessions    []SessionCost `json:"sessions"`
	Unreadable  int           `json:"unreadable,omitempty"`
}

// ModelCost is one model's share of the workspace, newest use last.
type ModelCost struct {
	ModelID      string  `json:"model_id"`
	ProviderID   string  `json:"provider_id,omitempty"`
	Requests     int     `json:"requests"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedTokens int64   `json:"cached_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	UnknownCost  int     `json:"unknown_cost,omitempty"`
	LastUsed     int64   `json:"last_used_unix_ns,omitempty"`
}

// DayCost is one local calendar day. Day is formatted 2006-01-02.
type DayCost struct {
	Day      string  `json:"day"`
	Requests int     `json:"requests"`
	CostUSD  float64 `json:"cost_usd"`
}

// SessionCost is one top-level session with its sub-agents.
type SessionCost struct {
	SessionID string  `json:"session_id"`
	Requests  int     `json:"requests"`
	CostUSD   float64 `json:"cost_usd"`
	First     int64   `json:"first_unix_ns,omitempty"`
	Last      int64   `json:"last_unix_ns,omitempty"`
}

// UsageReportDays is how many local days the daily series covers.
const UsageReportDays = 30

// UsageReader reads usage reports for one workspace folder. It remembers what
// it read from each database by size and modification time: closed sessions
// never change, so a later report reads only the databases still written.
type UsageReader struct {
	dir string
	mu  sync.Mutex
	dbs map[string]databaseUsage
}

type databaseUsage struct {
	size    int64
	modTime time.Time
	// day keys are local days at the time of the read; a new day reads again.
	day        string
	session    string
	models     map[modelKey]ModelCost
	days       map[string]DayCost
	first      int64
	last       int64
	requests   int
	cost       float64
	unknown    int
	unreadable bool
}

type modelKey struct{ model, provider string }

// NewUsageReader reads the session databases below workspaceDir.
func NewUsageReader(workspaceDir string) *UsageReader {
	return &UsageReader{dir: workspaceDir, dbs: map[string]databaseUsage{}}
}

// Report reads every session database once per change and sums them.
func (r *UsageReader) Report(ctx context.Context) (UsageReport, error) {
	paths, err := sessionDatabases(filepath.Join(r.dir, "sessions"))
	if err != nil {
		return UsageReport{}, err
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	_, offset := now.Zone()
	cutoff := now.AddDate(0, 0, -(UsageReportDays - 1))
	cutoff = time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, cutoff.Location())
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]bool, len(paths))
	for _, db := range paths {
		if err := ctx.Err(); err != nil {
			return UsageReport{}, err
		}
		seen[db.path] = true
		info, err := os.Stat(db.path)
		if err != nil {
			continue
		}
		size, modTime := info.Size(), info.ModTime()
		if wal, err := os.Stat(db.path + "-wal"); err == nil {
			size += wal.Size()
			if wal.ModTime().After(modTime) {
				modTime = wal.ModTime()
			}
		}
		cached, ok := r.dbs[db.path]
		if ok && cached.size == size && cached.modTime.Equal(modTime) && cached.day == today {
			continue
		}
		usage := readDatabaseUsage(ctx, db.path, cutoff.UnixNano(), offset)
		usage.size, usage.modTime, usage.day, usage.session = size, modTime, today, db.session
		r.dbs[db.path] = usage
	}
	for path := range r.dbs {
		if !seen[path] {
			delete(r.dbs, path)
		}
	}
	return r.sum(now, cutoff), nil
}

func (r *UsageReader) sum(now, cutoff time.Time) UsageReport {
	report := UsageReport{GeneratedAt: now.UnixNano()}
	models := map[modelKey]ModelCost{}
	days := map[string]DayCost{}
	sessions := map[string]SessionCost{}
	for _, db := range r.dbs {
		if db.unreadable {
			report.Unreadable++
			continue
		}
		report.Requests += db.requests
		report.CostUSD += db.cost
		report.UnknownCost += db.unknown
		report.First = earliest(report.First, db.first)
		report.Last = max(report.Last, db.last)
		for key, model := range db.models {
			total := models[key]
			total.ModelID, total.ProviderID = key.model, key.provider
			total.Requests += model.Requests
			total.InputTokens += model.InputTokens
			total.OutputTokens += model.OutputTokens
			total.CachedTokens += model.CachedTokens
			total.CostUSD += model.CostUSD
			total.UnknownCost += model.UnknownCost
			total.LastUsed = max(total.LastUsed, model.LastUsed)
			models[key] = total
		}
		for key, day := range db.days {
			total := days[key]
			total.Day = key
			total.Requests += day.Requests
			total.CostUSD += day.CostUSD
			days[key] = total
		}
		if db.requests > 0 {
			session := sessions[db.session]
			session.SessionID = db.session
			session.Requests += db.requests
			session.CostUSD += db.cost
			session.First = earliest(session.First, db.first)
			session.Last = max(session.Last, db.last)
			sessions[db.session] = session
		}
	}
	for _, model := range models {
		report.Models = append(report.Models, model)
	}
	sort.Slice(report.Models, func(i, j int) bool {
		a, b := report.Models[i], report.Models[j]
		if a.CostUSD != b.CostUSD {
			return a.CostUSD > b.CostUSD
		}
		return a.Requests > b.Requests
	})
	// Every day of the window is present, so a chart shows quiet days too.
	for day := cutoff; !day.After(now); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		value := days[key]
		value.Day = key
		report.Days = append(report.Days, value)
	}
	for _, session := range sessions {
		report.Sessions = append(report.Sessions, session)
	}
	sort.Slice(report.Sessions, func(i, j int) bool { return report.Sessions[i].Last > report.Sessions[j].Last })
	return report
}

func earliest(a, b int64) int64 {
	if a == 0 || b != 0 && b < a {
		return b
	}
	return a
}

// readDatabaseUsage reads one database read-only. A database that cannot be
// read is reported, never guessed: it may belong to a newer Crude.
func readDatabaseUsage(ctx context.Context, path string, cutoff int64, offset int) databaseUsage {
	usage := databaseUsage{models: map[modelKey]ModelCost{}, days: map[string]DayCost{}}
	db, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, "mode=ro&_busy_timeout=1000"))
	if err != nil {
		usage.unreadable = true
		return usage
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	const cost = `COALESCE(SUM(CASE WHEN cost_known=1 THEN cost_usd ELSE 0 END),0)`
	rows, err := db.QueryContext(ctx, `SELECT model_id, provider_id, COUNT(*), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0), COALESCE(SUM(cached_tokens),0), `+cost+`, COALESCE(SUM(cost_known=0),0), COALESCE(MIN(created_at),0), COALESCE(MAX(created_at),0) FROM usage_requests GROUP BY model_id, provider_id`)
	if err != nil {
		usage.unreadable = !missingUsageTable(err)
		return usage
	}
	for rows.Next() {
		var model ModelCost
		var first int64
		if err := rows.Scan(&model.ModelID, &model.ProviderID, &model.Requests, &model.InputTokens, &model.OutputTokens, &model.CachedTokens, &model.CostUSD, &model.UnknownCost, &first, &model.LastUsed); err != nil {
			_ = rows.Close()
			usage.unreadable = true
			return usage
		}
		usage.models[modelKey{model.ModelID, model.ProviderID}] = model
		usage.requests += model.Requests
		usage.cost += model.CostUSD
		usage.unknown += model.UnknownCost
		usage.first = earliest(usage.first, first)
		usage.last = max(usage.last, model.LastUsed)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		usage.unreadable = true
		return usage
	}
	rows, err = db.QueryContext(ctx, `SELECT date(created_at/1000000000 + ?, 'unixepoch'), COUNT(*), `+cost+` FROM usage_requests WHERE created_at >= ? GROUP BY 1`, offset, cutoff)
	if err != nil {
		usage.unreadable = true
		return usage
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var day DayCost
		if err := rows.Scan(&day.Day, &day.Requests, &day.CostUSD); err != nil {
			usage.unreadable = true
			return usage
		}
		usage.days[day.Day] = day
	}
	if rows.Err() != nil {
		usage.unreadable = true
	}
	return usage
}

// missingUsageTable is a session that never recorded a request.
func missingUsageTable(err error) bool {
	return strings.Contains(err.Error(), "no such table")
}

type sessionDatabase struct{ path, session string }

// sessionDatabases lists top-level session databases and the sub-agent
// databases kept in each session's folder.
func sessionDatabases(root string) ([]sessionDatabase, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []sessionDatabase
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && strings.HasSuffix(name, ".db") {
			out = append(out, sessionDatabase{path: filepath.Join(root, name), session: strings.TrimSuffix(name, ".db")})
			continue
		}
		if !entry.IsDir() {
			continue
		}
		children, err := os.ReadDir(filepath.Join(root, name, "subagents"))
		if err != nil {
			continue
		}
		for _, child := range children {
			if child.Type().IsRegular() && strings.HasSuffix(child.Name(), ".db") {
				out = append(out, sessionDatabase{path: filepath.Join(root, name, "subagents", child.Name()), session: name})
			}
		}
	}
	return out, nil
}
