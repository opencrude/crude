package diffview

import (
	"context"

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
	if msg.err != nil {
		return false, msg.err
	}
	msg.owner.geometry = msg.geometry
	return true, nil
}

func (source SourceLines) ReflowCommand(ctx context.Context) tea.Cmd {
	if source.owner == nil {
		return nil
	}
	source.owner.generation++
	owner, generation, lines := source.owner, source.owner.generation, source.owner.lines
	return func() tea.Msg {
		msg := SourceGeometryReadyMsg{owner: owner, generation: generation}
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

func (s *Screen) SourceGeometry() []SourceLines {
	return []SourceLines{s.prepared.beforeSource, s.prepared.afterSource}
}
