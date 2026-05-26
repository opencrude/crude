package surface

// ClipStack implements a scissor-clip stack for hierarchical clipping.
//
// When rendering nested containers, each container pushes its clip rect
// onto the stack. All drawing operations are clipped to the intersection
// of all active clip rects. When the container is done, it pops the clip.
//
// This is the same pattern used by GPU scissor testing and OpenTUI.
type ClipStack struct {
	stack []Rect
}

// NewClipStack creates an empty clip stack.
func NewClipStack() *ClipStack {
	return &ClipStack{
		stack: make([]Rect, 0, 8),
	}
}

// infiniteRect is returned by Active when the stack is empty,
// effectively disabling clipping.
var infiniteRect = Rect{X: -1e9, Y: -1e9, W: 2e9, H: 2e9}

// Push adds a clip rectangle. The new active clip is the intersection
// of the pushed rect and the previous active clip.
// If the intersection is empty, the empty rect is still pushed
// (so Pop works correctly) and Contains will return false for everything.
func (cs *ClipStack) Push(r Rect) {
	active := cs.Active()
	intersected := active.Intersect(r)
	cs.stack = append(cs.stack, intersected)
}

// Pop removes the top clip rectangle.
// It is safe to call Pop on an empty stack (no-op).
func (cs *ClipStack) Pop() {
	if len(cs.stack) > 0 {
		cs.stack = cs.stack[:len(cs.stack)-1]
	}
}

// Active returns the current effective clip rectangle.
// If the stack is empty, returns a maximally-sized rect (no clipping).
func (cs *ClipStack) Active() Rect {
	if len(cs.stack) == 0 {
		return infiniteRect
	}
	return cs.stack[len(cs.stack)-1]
}

// Contains reports whether a point is inside the active clip region.
func (cs *ClipStack) Contains(x, y int) bool {
	return cs.Active().Contains(x, y)
}

// Reset clears the entire clip stack.
func (cs *ClipStack) Reset() {
	cs.stack = cs.stack[:0]
}

// Depth returns the current stack depth.
func (cs *ClipStack) Depth() int {
	return len(cs.stack)
}
