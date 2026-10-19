package diffview

import (
	"context"
	"fmt"
	"iter"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/textsearch"
)

const searchMatchLimit = 10_000

type SearchLine struct {
	Row, Side int
	Text      string
}
type SearchHit struct{ Row, Side, Start, End int }

// SearchProjection is shared by Git and local readers. Immutable source and
// presentation snapshots are scanned off Update; exact-job publication cannot
// overwrite a newer query, source, fold or split geometry.
type SearchProjection struct {
	Query   string
	Matches []SearchHit
	ByRow   map[int][]int
	Active  int
	Error   string
	job     *searchJob
	jobs    map[*searchJob]struct{}
}
type searchJob struct {
	done       chan struct{}
	cancel     context.CancelFunc
	delivering bool
	hits       []SearchHit
	byRow      map[int][]int
	err        error
	onReady    func()
}
type SearchReadyMsg struct {
	owner *SearchProjection
	job   *searchJob
}

func (m SearchReadyMsg) Apply() bool {
	if m.owner == nil || m.owner.job != m.job {
		return false
	}
	m.owner.job = nil
	m.owner.Matches, m.owner.ByRow = m.job.hits, m.job.byRow
	m.owner.Active = -1
	m.owner.Error = ""
	if m.job.err != nil {
		m.owner.Error = "Search unavailable: " + m.job.err.Error()
		m.owner.Matches, m.owner.ByRow = nil, nil
	}
	if len(m.job.hits) > 0 {
		m.owner.Active = 0
	}
	if m.job.onReady != nil {
		m.job.onReady()
	}
	return true
}
func (s *SearchProjection) Reset(query string, lines iter.Seq[SearchLine]) {
	s.Cancel()
	s.Query, s.Matches, s.ByRow, s.Active, s.Error = query, nil, nil, -1, ""
	if query == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	job := &searchJob{done: make(chan struct{}), cancel: cancel}
	s.job = job
	if s.jobs == nil {
		s.jobs = make(map[*searchJob]struct{})
	}
	for old := range s.jobs {
		select {
		case <-old.done:
			delete(s.jobs, old)
		default:
		}
	}
	s.jobs[job] = struct{}{}
	go func() {
		defer close(job.done)
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				job.err = fmt.Errorf("comparison search failed: %v", recovered)
			}
		}()
		release, err := AcquirePreparation(ctx)
		if err != nil {
			job.err = err
			return
		}
		defer release()
		matcher, err := textsearch.Compile(query, textsearch.Options{})
		if err != nil {
			job.err = err
			return
		}
		job.byRow = make(map[int][]int)
		for line := range lines {
			if err := ctx.Err(); err != nil {
				job.err = err
				return
			}
			hits, err := sourceMatches(ctx, line.Text, matcher, searchMatchLimit-len(job.hits))
			if err != nil {
				job.err = err
				return
			}
			for _, hit := range hits {
				job.byRow[line.Row] = append(job.byRow[line.Row], len(job.hits))
				job.hits = append(job.hits, SearchHit{Row: line.Row, Side: line.Side, Start: hit.Start, End: hit.End})
			}
			if len(job.hits) >= searchMatchLimit {
				return
			}
		}
	}()
}
func (s *SearchProjection) Cancel() {
	for job := range s.jobs {
		job.cancel()
	}
	s.job = nil
}

// Suspend invalidates column-based highlights until new source geometry is
// published. Keep the query for the owner's subsequent background rescan.
func (s *SearchProjection) Suspend() {
	s.Cancel()
	s.Matches, s.ByRow, s.Active, s.Error = nil, nil, -1, ""
}

func (s *SearchProjection) Wait() {
	s.Cancel()
	for job := range s.jobs {
		<-job.done
		delete(s.jobs, job)
	}
}
func (s *SearchProjection) Done() <-chan struct{} {
	if s.job != nil {
		return s.job.done
	}
	return nil
}
func (s *SearchProjection) Command(onReady func()) tea.Cmd {
	job := s.job
	if job == nil || job.delivering {
		return nil
	}
	job.delivering = true
	job.onReady = onReady
	return func() tea.Msg { <-job.done; return SearchReadyMsg{owner: s, job: job} }
}
func (s *SearchProjection) Status() string {
	if s.Query == "" {
		return ""
	}
	if s.job != nil {
		return "Searching comparison…"
	}
	if s.Error != "" {
		return s.Error
	}
	if len(s.Matches) == 0 {
		return "No matches"
	}
	if len(s.Matches) >= searchMatchLimit {
		return fmt.Sprintf("%d / first %d matches · narrow the query", s.Active+1, len(s.Matches))
	}
	return fmt.Sprintf("%d / %d matches", s.Active+1, len(s.Matches))
}
