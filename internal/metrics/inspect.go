package metrics

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"crude/types/inspect"
)

// InspectQuery selects demand-collected process evidence. Page.Next can be
// supplied as Page on continuation, with Seconds and Points omitted.
type InspectQuery struct {
	Page    inspect.Request `json:"page"`
	Seconds int             `json:"seconds,omitempty"`
	Points  int             `json:"points,omitempty"`
}

var processIncarnation = rand.Text()
var hostOnce sync.Once
var hostName, hostBoot string

func hostProvenance() (string, string, string) {
	hostOnce.Do(func() {
		hostName, _ = os.Hostname()
		if runtime.GOOS == "linux" {
			if raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
				hostBoot = strings.TrimSpace(string(raw))
			}
		}
	})
	identity := "unknown"
	if hostBoot != "" {
		identity = "boot_id"
	}
	return hostName, hostBoot, identity
}

func matchesMetric(meta Meta, owner, filter string) bool {
	filter = strings.ToLower(filter)
	return filter == "" || strings.Contains(strings.ToLower(meta.Name), filter) ||
		strings.Contains(strings.ToLower(groupOf(meta)), filter) || strings.Contains(strings.ToLower(owner), filter)
}

// Inspect reads one immutable bounded observation from this process's registry.
func Inspect(ctx context.Context, store *inspect.Store, owner string, query InspectQuery) (inspect.Page, error) {
	return InspectWith(ctx, store, owner, query, nil)
}

// InspectWith lets a neutral consumer append interpretations of the same
// snapshot to summary evidence. It never runs for retained continuation pages.
func InspectWith(ctx context.Context, store *inspect.Store, owner string, query InspectQuery, decorate func(Snapshot) []inspect.Record) (inspect.Page, error) {
	if store == nil {
		return inspect.Page{}, errors.New("process inspection store is required")
	}
	request, err := query.Page.Normalize()
	if err != nil {
		return inspect.Page{}, err
	}
	if query.Seconds < 0 || query.Seconds > HistorySeconds || query.Points < 0 || query.Points > 60 {
		return inspect.Page{}, errors.New("seconds must be 1–300 (omit for 120); points must be 0–60 (0 omits history); echo next as page for continuation")
	}
	if request.SnapshotID == "" && request.View == "" {
		request.View = "summary"
	}
	switch request.View {
	case "", "summary", "series", "history", "tables", "events", "resources":
	default:
		return inspect.Page{}, errors.New("view must be summary, series, history, tables, events or resources")
	}
	if request.SnapshotID == "" && request.View == "history" && query.Points == 0 {
		return inspect.Page{}, errors.New("history requires points 1–60; use series for summaries without points")
	}
	seconds := query.Seconds
	if seconds == 0 {
		seconds = 120
	}
	page, err := store.Read(ctx, owner, request, func(ctx context.Context) (inspect.Observation, error) {
		points := query.Points
		// Peak busy and gauge edge facts are independent of display geometry;
		// summary delivery needs no full chart history to preserve diagnosis.
		if points == 0 {
			points = 1
		}
		snapshot, err := takeContext(ctx, Query{Seconds: seconds, Points: points}, snapshotOptions{all: true,
			resources: request.View == "summary" || request.View == "resources", filter: request.Filter})
		if err != nil {
			return inspect.Observation{}, err
		}
		return inspectionObservation(ctx, snapshot, request, query.Points, decorate)
	})
	if err != nil {
		return inspect.Page{}, err
	}
	var source struct {
		Seconds int `json:"seconds"`
		Points  int `json:"requested_points"`
	}
	if err := json.Unmarshal(page.Source, &source); err != nil {
		return inspect.Page{}, err
	}
	if query.Seconds != 0 && query.Seconds != source.Seconds || query.Points != 0 && query.Points != source.Points {
		return inspect.Page{}, errors.New("snapshot seconds/points changed; omit them on continuation or start a fresh read")
	}
	return page, nil
}

type inspectionSource struct {
	Process           string    `json:"process"`
	PID               int       `json:"pid"`
	Incarnation       string    `json:"incarnation"`
	Started           time.Time `json:"started"`
	Hostname          string    `json:"hostname,omitempty"`
	HostBootID        string    `json:"host_boot_id,omitempty"`
	HostIdentity      string    `json:"host_identity"`
	Taken             time.Time `json:"taken"`
	Completed         time.Time `json:"completed"`
	First             int64     `json:"first"`
	End               int64     `json:"end"`
	Seconds           int       `json:"seconds"`
	RecentSeconds     int       `json:"recent_seconds"`
	RequestedPoints   int       `json:"requested_points"`
	Step              int       `json:"step"`
	ActualPoints      int       `json:"actual_points"`
	Filter            string    `json:"filter,omitempty"`
	EventsOverwritten uint64    `json:"events_overwritten_lifetime"`
}

func inspectionObservation(ctx context.Context, s Snapshot, r inspect.Request, points int, decorate func(Snapshot) []inspect.Record) (inspect.Observation, error) {
	actual := 0
	if points > 0 {
		actual = (s.Seconds + s.Step - 1) / s.Step
	}
	source, err := json.Marshal(inspectionSource{Process: s.Process, PID: s.PID, Incarnation: s.Incarnation,
		Started: s.Started, Hostname: s.Hostname, HostBootID: s.HostBootID, HostIdentity: s.HostIdentity,
		Taken: s.Taken, Completed: s.Completed, First: s.First, End: s.End, Seconds: s.Seconds,
		RecentSeconds: min(s.Seconds, recentSeconds), RequestedPoints: points, Step: s.Step, ActualPoints: actual,
		Filter: r.Filter, EventsOverwritten: s.EventsOverwritten})
	if err != nil {
		return inspect.Observation{}, err
	}
	out := inspect.Observation{Source: source, Notes: []string{
		"Per-series lock-consistent capture, not a globally atomic instant. Durations are microseconds; percentiles estimate histogram bucket upper bounds clamped to the observed maximum. Totals are lifetime, window/recent values use the selected seconds.",
		"Series retain 300 seconds; tables retain 120 seconds and fold excess keys into other. Events retain the newest 256 and overwritten count is lifetime, not selected-window loss. The final point can be shorter than step. Hostname alone does not establish host equivalence.",
	}}
	add := func(kind, id string, value any) {
		if err != nil {
			return
		}
		raw, e := json.Marshal(value)
		if e != nil {
			err = e
			return
		}
		out.Records = append(out.Records, inspect.Record{Kind: kind, ID: id, Value: raw})
	}
	metricViews := r.View == "summary" || r.View == "series" || r.View == "history"
	point := func(kind, name string, i int, value any) {
		first := s.First + int64(i*s.Step)
		add(kind, fmt.Sprintf("%s/%d", name, i), struct {
			Series  string `json:"series"`
			Index   int    `json:"index"`
			First   int64  `json:"first"`
			Seconds int    `json:"seconds"`
			Value   any    `json:"value"`
		}{name, i, first, min(s.Step, int(s.End-first+1)), value})
	}
	if r.View == "summary" {
		if busy, ok := UIBusy(s); ok {
			busy.Points = nil
			add("ui_busy", "ui.busy", busy)
		}
		if decorate != nil {
			out.Records = append(out.Records, decorate(s)...)
		}
	}
	if metricViews {
		for _, v := range s.Latencies {
			values := v.Points
			v.Points = nil
			add("latency", v.Meta.Name, struct {
				LatencySeries
				Observed bool `json:"observed"`
			}{v, v.Total > 0})
			if points > 0 {
				for i, p := range values {
					point("latency_point", v.Meta.Name, i, p)
				}
			}
		}
		for _, v := range s.Counters {
			values := v.Points
			v.Points = nil
			add("counter", v.Meta.Name, struct {
				CounterSeries
				Observed bool `json:"observed"`
			}{v, v.Total > 0})
			if points > 0 {
				for i, p := range values {
					point("counter_point", v.Meta.Name, i, p)
				}
			}
		}
		for _, v := range s.Gauges {
			values := v.Points
			v.Points = nil
			add("gauge", v.Meta.Name, v)
			if points > 0 {
				for i, p := range values {
					point("gauge_point", v.Meta.Name, i, p)
				}
			}
		}
	}
	if metricViews || r.View == "tables" {
		for _, v := range s.Tables {
			add("table", v.Meta.Name, struct {
				Meta            Meta   `json:"meta"`
				Rows            int    `json:"rows"`
				Folded          uint64 `json:"folded_observations"`
				RetainedSeconds int    `json:"retained_seconds"`
			}{v.Meta, len(v.Rows), v.Folded, min(s.Seconds, tableSeconds)})
			if r.View != "tables" && r.View != "history" {
				continue
			}
			for _, row := range v.Rows {
				values := row.Points
				row.Points = nil
				id := v.Meta.Name + "/" + row.Key
				add("table_row", id, struct {
					Series string `json:"series"`
					TableRow
				}{v.Meta.Name, row})
				if points > 0 {
					for i, p := range values {
						point("table_point", id, i, p)
					}
				}
			}
		}
	}
	if r.View == "events" {
		for i, v := range s.Events {
			if !matchesMetric(Meta{Name: v.Series, Group: v.Group}, s.Process, r.Filter) {
				continue
			}
			add("event", fmt.Sprint(i), v)
		}
	}
	if s.Resources != nil {
		appendResourceRecords(s.Resources, r.View == "resources", add)
	}
	if e := ctx.Err(); e != nil {
		return inspect.Observation{}, e
	}
	return out, err
}

func appendResourceRecords(r *Resources, detailed bool, add func(string, string, any)) {
	summary := *r
	summary.Processes = nil
	summary.Disk = nil
	add("resources", "resources", summary)
	if r.Disk != nil {
		disk := *r.Disk
		disk.Parts = nil
		disk.Workspaces = nil
		add("disk", "disk", disk)
	}
	if !detailed {
		return
	}
	for _, p := range r.Processes {
		add("process", fmt.Sprintf("%d/%d", p.PID, p.CreatedMS), p)
	}
	if r.Disk != nil {
		for _, p := range r.Disk.Parts {
			add("disk_part", p.Name, p)
		}
		for _, w := range r.Disk.Workspaces {
			add("workspace_disk", w.ID, w)
		}
	}
}

// BusyUsage is event-loop work, not OS CPU. It excludes ui.blocking because
// blocking durations already belong to ui.update/ui.view and would double count.
type BusyUsage struct {
	Window        float64   `json:"window_percent"`
	Peak          float64   `json:"peak_percent"`
	Recent        float64   `json:"recent_percent"`
	RecentSeconds int       `json:"recent_seconds"`
	Points        []float64 `json:"points,omitempty"`
}

func UIBusy(s Snapshot) (BusyUsage, bool) {
	out := BusyUsage{RecentSeconds: min(s.Seconds, recentSeconds), Peak: s.UIBusyPeak}
	found := false
	for _, v := range s.Latencies {
		if v.Meta.Name != "ui.update" && v.Meta.Name != "ui.view" {
			continue
		}
		if v.Total > 0 {
			found = true
		}
		out.Window += float64(v.Window.Sum)
		out.Recent += float64(v.Recent.Sum)
		for i, p := range v.Points {
			if len(out.Points) <= i {
				out.Points = append(out.Points, make([]float64, i+1-len(out.Points))...)
			}
			width := min(max(s.Step, 1), s.Seconds-i*max(s.Step, 1))
			if width > 0 {
				out.Points[i] += float64(p.Sum) / float64(width*1_000_000) * 100
			}
		}
	}
	if s.Seconds > 0 {
		out.Window = out.Window / float64(s.Seconds*1_000_000) * 100
	}
	if out.RecentSeconds > 0 {
		out.Recent = out.Recent / float64(out.RecentSeconds*1_000_000) * 100
	}
	return out, found
}
