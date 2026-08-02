package widget

import (
	"testing"

	"crude/tui/surface"
)

// fixedWidget reports a constant preferred size and renders nothing.
type fixedWidget struct{ w, h int }

func (f fixedWidget) Render(_ *surface.Surface, _ *RenderContext) {}
func (f fixedWidget) Measure(_, _ int) (int, int)                { return f.w, f.h }

func TestStackLayout_VerticalArrangesTopToBottom(t *testing.T) {
	kids := []ChildWidget{
		{Widget: fixedWidget{w: 10, h: 3}, Visible: true},
		{Widget: fixedWidget{w: 10, h: 2}, Visible: true},
	}
	(&StackLayout{Direction: Vertical, Gap: 1}).Arrange(kids, 20, 20)

	if kids[0].X != 0 || kids[0].Y != 0 || kids[0].W != 20 || kids[0].H != 3 {
		t.Fatalf("child0=%+v want X=0 Y=0 W=20 H=3", kids[0])
	}
	// Last child takes the remaining space (gap of 1 between children).
	if kids[1].X != 0 || kids[1].Y != 4 || kids[1].W != 20 || kids[1].H != 16 {
		t.Fatalf("child1=%+v want X=0 Y=4 W=20 H=16 (last child takes remaining)", kids[1])
	}
}

func TestStackLayout_VerticalLastChildTakesRemainingSpace(t *testing.T) {
	kids := []ChildWidget{
		{Widget: fixedWidget{w: 10, h: 2}, Visible: true},
		{Widget: fixedWidget{w: 10, h: 2}, Visible: true},
	}
	(&StackLayout{Direction: Vertical, Gap: 0}).Arrange(kids, 20, 10)

	if kids[1].Y != 2 || kids[1].H != 8 {
		t.Fatalf("last child=%+v want Y=2 H=8 (remaining space)", kids[1])
	}
}

func TestStackLayout_VerticalClampsToAvailable(t *testing.T) {
	kids := []ChildWidget{
		{Widget: fixedWidget{w: 10, h: 100}, Visible: true}, // pref > avail
	}
	(&StackLayout{Direction: Vertical, Gap: 0}).Arrange(kids, 20, 5)

	if kids[0].H != 5 {
		t.Fatalf("child=%+v want H=5 (clamped to available)", kids[0])
	}
}

func TestStackLayout_HorizontalArrangesLeftToRight(t *testing.T) {
	kids := []ChildWidget{
		{Widget: fixedWidget{w: 3, h: 2}, Visible: true},
		{Widget: fixedWidget{w: 4, h: 2}, Visible: true},
	}
	(&StackLayout{Direction: Horizontal, Gap: 2}).Arrange(kids, 20, 20)

	if kids[0].X != 0 || kids[0].Y != 0 || kids[0].W != 3 || kids[0].H != 20 {
		t.Fatalf("child0=%+v want X=0 Y=0 W=3 H=20", kids[0])
	}
	// Last child takes the remaining width (gap of 2 between children).
	if kids[1].X != 5 || kids[1].Y != 0 || kids[1].W != 15 || kids[1].H != 20 {
		t.Fatalf("child1=%+v want X=5 Y=0 W=15 H=20 (last child takes remaining)", kids[1])
	}
}

func TestStackLayout_HorizontalLastChildTakesRemainingSpace(t *testing.T) {
	kids := []ChildWidget{
		{Widget: fixedWidget{w: 2, h: 2}, Visible: true},
		{Widget: fixedWidget{w: 2, h: 2}, Visible: true},
	}
	(&StackLayout{Direction: Horizontal, Gap: 0}).Arrange(kids, 10, 10)

	if kids[1].X != 2 || kids[1].W != 8 {
		t.Fatalf("last child=%+v want X=2 W=8 (remaining space)", kids[1])
	}
}

func TestStackLayout_EmptyChildrenNoop(t *testing.T) {
	(&StackLayout{Direction: Vertical, Gap: 1}).Arrange(nil, 10, 10) // must not panic
	(&StackLayout{Direction: Horizontal, Gap: 1}).Arrange([]ChildWidget{}, 10, 10)
}
