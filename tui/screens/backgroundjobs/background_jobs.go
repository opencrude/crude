// Package backgroundjobs provides the passive background-command detail panel.
package backgroundjobs

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID ui.ComponentID = "session-background-jobs"
	FormID   ui.ComponentID = "session-background-jobs/form"
)

const completedRetention = 15 * time.Second

type Job struct {
	Handle    string
	Command   string
	PID       int
	Status    string
	StartedAt time.Time
	EndedAt   time.Time
	Duration  time.Duration
	ExitCode  int
	Error     string
}

type Screen struct {
	ui.Base
	tokens     *ui.TokenSet
	jobs       []Job
	visible    int
	attention  bool
	lastSecond int64
	form       *ui.Form
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th)}
	s.rebuild(time.Now())
	return s
}

func (s *Screen) SetJobs(jobs []Job, now time.Time) {
	s.jobs = append(s.jobs[:0], jobs...)
	s.rebuild(now)
}

func (s *Screen) Refresh(now time.Time) bool {
	visible := s.visibleJobs(now)
	running := false
	for _, job := range visible {
		if job.Status == "running" {
			running = true
			break
		}
	}
	if len(visible) == s.visible && (!running || now.Unix() == s.lastSecond) {
		return false
	}
	s.rebuild(now)
	return true
}

func (s *Screen) VisibleCount() int { return s.visible }

func (s *Screen) NeedsAttention() bool {
	return s.attention
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.form.SetTokens(tokens)
}

func (s *Screen) Children() []ui.Component { return []ui.Component{s.form} }
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: s.PreferredHeight(cs.MaxH)})
}
func (s *Screen) PreferredHeight(maxHeight int) int {
	return min(maxHeight, s.form.PreferredHeight(maxHeight))
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.form.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (s *Screen) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}
func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult      { return ui.EventResult{} }
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Background jobs", Value: fmt.Sprintf("%d visible", s.visible)}
}

func (s *Screen) rebuild(now time.Time) {
	visible := s.visibleJobs(now)
	model := ui.NewFormModel()
	for index, job := range visible {
		value := ui.StringValue(job.Command)
		model.AddField(&ui.FieldState{
			ID: fmt.Sprintf("%s-%d", job.Handle, index), Label: "[" + job.Handle + "]",
			Type: ui.FieldText, Value: value, Initial: value, ReadOnly: true,
			Description: jobDetail(job, now),
		})
	}
	detail := fmt.Sprintf("%d command", len(visible))
	if len(visible) != 1 {
		detail += "s"
	}
	s.form = ui.NewDockedForm(FormID, model, "Background jobs", detail, s.tokens)
	s.form.SetCompact(false)
	s.visible = len(visible)
	s.attention = false
	for _, job := range visible {
		if job.Status == "failed" || job.Status == "killed" {
			s.attention = true
			break
		}
	}
	s.lastSecond = now.Unix()
}

func (s *Screen) visibleJobs(now time.Time) []Job {
	visible := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		if job.Status != "running" && !job.EndedAt.IsZero() && now.Sub(job.EndedAt) > completedRetention {
			continue
		}
		visible = append(visible, job)
	}
	sort.Slice(visible, func(i, j int) bool {
		if visible[i].StartedAt.Equal(visible[j].StartedAt) {
			return visible[i].Handle < visible[j].Handle
		}
		return visible[i].StartedAt.Before(visible[j].StartedAt)
	})
	return visible
}

func jobDetail(job Job, now time.Time) string {
	duration := job.Duration
	if job.Status == "running" && !job.StartedAt.IsZero() {
		duration = now.Sub(job.StartedAt)
	}
	parts := []string{strings.ToUpper(job.Status), duration.Truncate(time.Second).String()}
	if job.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", job.PID))
	}
	if job.Status != "running" {
		parts = append(parts, fmt.Sprintf("exit %d", job.ExitCode))
	}
	if job.Error != "" {
		parts = append(parts, job.Error)
	}
	return strings.Join(parts, " | ")
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
