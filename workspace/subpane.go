package workspace

import "time"

type SubPane struct {
	ID    string
	Title string
	Type  PaneType
	// Messages is canonical workspace data.
	Messages   *Conversation
	Done       bool
	StartTime  time.Time
	EndTime    time.Time
	TokenCount int
	Status     string // child outcome; empty for thinking panes

	compactScrollY    int
	compactContentH   int
	compactVisibleH   int
	compactAutoScroll bool
}

func NewSubPane(id string, paneType PaneType, title string) *SubPane {
	return &SubPane{
		ID:                id,
		Title:             title,
		Type:              paneType,
		Messages:          NewConversation(),
		Done:              false,
		StartTime:         time.Now(),
		compactAutoScroll: true,
	}
}

func (sp *SubPane) Elapsed() time.Duration {
	if sp.Done && !sp.EndTime.IsZero() {
		return sp.EndTime.Sub(sp.StartTime)
	}
	return time.Since(sp.StartTime)
}

func (sp *SubPane) Collapse() {
	if sp.Done {
		return
	}
	sp.Done = true
	sp.EndTime = time.Now()
}

func (sp *SubPane) CanScrollCompact() bool {
	if sp.Done || sp.Type != PaneAgent {
		return false
	}
	return sp.compactContentH > sp.compactVisibleH && sp.compactVisibleH > 0
}

func (sp *SubPane) ScrollCompactUp() {
	sp.compactAutoScroll = false
	if sp.compactScrollY > 0 {
		sp.compactScrollY--
	}
}

func (sp *SubPane) ScrollCompactDown() {
	maxScroll := sp.compactContentH - sp.compactVisibleH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if sp.compactScrollY < maxScroll {
		sp.compactScrollY++
	}
	if sp.compactScrollY >= maxScroll {
		sp.compactAutoScroll = true
	}
}

func (sp *SubPane) CompactScrollY() int {
	return sp.compactScrollY
}

func (sp *SubPane) SetCompactScrollY(y int) {
	sp.compactScrollY = y
}

func (sp *SubPane) CompactContentH() int {
	return sp.compactContentH
}

func (sp *SubPane) SetCompactContentH(h int) {
	sp.compactContentH = h
}

func (sp *SubPane) CompactVisibleH() int {
	return sp.compactVisibleH
}

func (sp *SubPane) SetCompactVisibleH(h int) {
	sp.compactVisibleH = h
}

func (sp *SubPane) CompactAutoScroll() bool {
	return sp.compactAutoScroll
}

func (sp *SubPane) SetCompactAutoScroll(v bool) {
	sp.compactAutoScroll = v
}

type PaneRenderResult struct {
	Lines         []string
	PaneID        string
	ContentStart  int
	ContentHeight int
}

type PaneManager struct {
	panes     map[string]*SubPane
	paneOrder []string
}

func NewPaneManager() *PaneManager {
	return &PaneManager{
		panes: make(map[string]*SubPane),
	}
}

func (pm *PaneManager) Get(id string) *SubPane {
	return pm.panes[id]
}

func (pm *PaneManager) GetOrCreate(id string, paneType PaneType, title string) *SubPane {
	if p, ok := pm.panes[id]; ok {
		return p
	}
	sp := NewSubPane(id, paneType, title)
	pm.panes[id] = sp
	pm.paneOrder = append(pm.paneOrder, id)
	return sp
}

func (pm *PaneManager) Has(id string) bool {
	_, ok := pm.panes[id]
	return ok
}

func (pm *PaneManager) CollapsePane(id string) {
	if sp, ok := pm.panes[id]; ok {
		sp.Collapse()
	}
}

func (pm *PaneManager) Remove(id string) {
	delete(pm.panes, id)
	for i, pid := range pm.paneOrder {
		if pid == id {
			pm.paneOrder = append(pm.paneOrder[:i], pm.paneOrder[i+1:]...)
			break
		}
	}
}

func (pm *PaneManager) HasAnyActive() bool {
	for _, sp := range pm.panes {
		if !sp.Done {
			return true
		}
	}
	return false
}

func (pm *PaneManager) Panes() map[string]*SubPane {
	return pm.panes
}

func (pm *PaneManager) PaneOrder() []string {
	return pm.paneOrder
}
