package diffview

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// SourceGeometryReadyMsg changes only geometry of its exact immutable source.
// A replacement source owns another identity; repeated calibration is versioned.
type SourceGeometryReadyMsg struct {
	owner      *sourceOwner
	generation uint64
	geometry   sourceGeometry
	err        error
}

func (msg SourceGeometryReadyMsg) Apply() (bool, error) {
	if msg.owner == nil || msg.owner.generation != msg.generation {
		return false, nil
	}
	msg.owner.pending, msg.owner.err = false, msg.err
	msg.owner.cancel = nil
	// A delivered completion is consumed once, even if a caller retains it.
	msg.owner.generation++
	if msg.err != nil {
		return false, msg.err
	}
	msg.owner.geometry = msg.geometry
	return true, nil
}

func (msg SourceGeometryReadyMsg) Source() SourceLines { return SourceLines{owner: msg.owner} }

// GeometryStatus fences consumers while old checkpoints describe a different
// terminal profile. Never paint or hit-test new widths against old columns.
func (source SourceLines) GeometryStatus() string {
	if source.owner == nil {
		return ""
	}
	if source.owner.pending {
		return "Calibrating comparison geometry…"
	}
	if source.owner.err != nil {
		return "Comparison geometry unavailable: " + source.owner.err.Error()
	}
	return ""
}

func (source SourceLines) ReflowCommand(ctx context.Context) tea.Cmd {
	if source.owner == nil {
		return nil
	}
	source.CancelReflow()
	ctx, cancel := context.WithCancel(ctx)
	source.owner.cancel = cancel
	source.owner.generation++
	source.owner.pending, source.owner.err = true, nil
	owner, generation, lines := source.owner, source.owner.generation, source.owner.lines
	return func() (result tea.Msg) {
		defer cancel()
		msg := SourceGeometryReadyMsg{owner: owner, generation: generation}
		defer func() {
			if recovered := recover(); recovered != nil {
				msg.err = fmt.Errorf("comparison geometry failed: %v", recovered)
			}
			result = msg
		}()
		release, err := AcquirePreparation(ctx)
		if err != nil {
			msg.err = err
			return msg
		}
		defer release()
		next, err := PrepareSourceLinesContext(ctx, lines)
		msg.err = err
		if err == nil {
			msg.geometry = next.owner.geometry
		}
		return msg
	}
}

// CancelReflow invalidates the queued/running source job. The application keeps
// join ownership; a shared source is cancelled only after its last reader drops it.
func (source SourceLines) CancelReflow() {
	if source.owner == nil || source.owner.cancel == nil {
		return
	}
	source.owner.cancel()
	source.owner.cancel = nil
	source.owner.generation++
}

func (s *Screen) SourceGeometry() []SourceLines {
	return []SourceLines{s.prepared.beforeSource, s.prepared.afterSource}
}

func (s *Screen) BeginGeometryReflow() {
	for _, p := range []*pane{s.before, s.after, s.unified} {
		p.CancelPointerCapture()
		p.selected, p.all = false, false
	}
	s.search.Suspend()
}

func (s *Screen) geometryStatus() string {
	if status := s.prepared.beforeSource.GeometryStatus(); status != "" {
		return status
	}
	return s.prepared.afterSource.GeometryStatus()
}
