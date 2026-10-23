package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"crude/metrics"
)

// DataUsage measures how much disk Crude's data folder takes, per part and
// per saved workspace. Measuring walks every saved file, so it runs in the
// background, at most once per interval and only when someone asks (the
// Performance surface while it is visible); Usage always returns at once.
type DataUsage struct {
	dir      string
	interval time.Duration

	mu      sync.Mutex
	latest  metrics.DiskUsage
	running bool
	// projects caches each workspace's project folder; it never changes for
	// a workspace ID, so each database is read once per process.
	projects map[string]string
}

// dataUsageWorkspaces is how many of the largest workspaces a measurement
// lists by name; the totals cover all of them.
const dataUsageWorkspaces = 12

func NewDataUsage(dataDir string) *DataUsage {
	return &DataUsage{dir: dataDir, interval: time.Minute, latest: metrics.DiskUsage{Path: dataDir}, projects: map[string]string{}}
}

// Usage returns the latest measurement and starts the next one when the
// latest is older than the interval.
func (u *DataUsage) Usage() *metrics.DiskUsage {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.running && (u.latest.MeasuredAt.IsZero() || time.Since(u.latest.MeasuredAt) >= u.interval) {
		u.running = true
		go u.measure()
	}
	out := u.latest
	out.Measuring = u.running
	return &out
}

func (u *DataUsage) measure() {
	result := u.scan()
	u.mu.Lock()
	u.latest, u.running = result, false
	u.mu.Unlock()
}

// partNames are readable names for the data folder's top-level entries.
var partNames = map[string]string{
	"workspaces":         "Workspaces",
	"deleted-workspaces": "Deleted workspaces (recoverable)",
	"backups":            "Backups",
	"bin":                "Crude programs",
	"sessions":           "sessions folder",
}

func (u *DataUsage) scan() metrics.DiskUsage {
	result := metrics.DiskUsage{Path: u.dir, MeasuredAt: time.Now()}
	if usage, err := disk.Usage(u.dir); err == nil {
		result.DeviceTotal, result.DeviceFree = int64(usage.Total), int64(usage.Free)
	}
	entries, err := os.ReadDir(u.dir)
	if err != nil {
		result.Err = err.Error()
		return result
	}
	var files int64
	for _, entry := range entries {
		path := filepath.Join(u.dir, entry.Name())
		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
				files += info.Size()
			}
			continue
		}
		var bytes int64
		if entry.Name() == "workspaces" {
			bytes = u.scanWorkspaces(path, &result)
		} else {
			bytes = treeSize(path)
		}
		name := partNames[entry.Name()]
		if name == "" {
			name = entry.Name() + " folder"
		}
		result.Parts = append(result.Parts, metrics.DiskPart{Name: name, Bytes: bytes})
		result.Total += bytes
	}
	if files > 0 {
		result.Parts = append(result.Parts, metrics.DiskPart{Name: "Databases and settings", Bytes: files})
		result.Total += files
	}
	slices.SortStableFunc(result.Parts, func(a, b metrics.DiskPart) int { return compareBytes(a.Bytes, b.Bytes) })
	return result
}

func (u *DataUsage) scanWorkspaces(root string, result *metrics.DiskUsage) int64 {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	var all []metrics.WorkspaceDisk
	var total int64
	for _, entry := range entries {
		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
				total += info.Size()
			}
			continue
		}
		dir := filepath.Join(root, entry.Name())
		ws := metrics.WorkspaceDisk{ID: entry.Name()}
		parts, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, part := range parts {
			path := filepath.Join(dir, part.Name())
			var bytes int64
			if part.IsDir() {
				bytes = treeSize(path)
			} else if info, err := part.Info(); err == nil && info.Mode().IsRegular() {
				bytes = info.Size()
			}
			ws.Bytes += bytes
			switch part.Name() {
			case "sessions":
				ws.Sessions = bytes
			case "local-history":
				ws.History = bytes
			case "executions":
				ws.Worktrees = bytes
			}
		}
		all = append(all, ws)
		total += ws.Bytes
	}
	u.resolveProjects(root, all)
	for i := range all {
		project := u.projectOf(all[i].ID)
		all[i].Project = project
		if project != "" {
			if _, err := os.Stat(project); errors.Is(err, fs.ErrNotExist) {
				all[i].Missing = true
				result.Orphaned++
				result.OrphanedBytes += all[i].Bytes
			}
		}
	}
	slices.SortStableFunc(all, func(a, b metrics.WorkspaceDisk) int { return compareBytes(a.Bytes, b.Bytes) })
	result.WorkspaceCount, result.WorkspaceBytes = len(all), total
	result.Workspaces = all[:min(len(all), dataUsageWorkspaces)]
	return total
}

// resolveProjects reads the project folder of every workspace not yet
// cached, a few databases at a time.
func (u *DataUsage) resolveProjects(root string, all []metrics.WorkspaceDisk) {
	var missing []string
	u.mu.Lock()
	for _, ws := range all {
		if _, ok := u.projects[ws.ID]; !ok {
			missing = append(missing, ws.ID)
		}
	}
	u.mu.Unlock()
	if len(missing) == 0 {
		return
	}
	ctx := context.Background()
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range min(8, len(missing)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				project := ""
				if info := readWorkspaceMetaBounded(ctx, filepath.Join(root, id, "workspace.db"), 150*time.Millisecond); info != nil {
					project = info.ProjectDir
				}
				u.mu.Lock()
				u.projects[id] = project
				u.mu.Unlock()
			}
		}()
	}
	for _, id := range missing {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
}

func (u *DataUsage) projectOf(id string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.projects[id]
}

// treeSize is the bytes of regular files under path; links are not followed.
func treeSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func compareBytes(a, b int64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}
