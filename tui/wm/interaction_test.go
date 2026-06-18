package wm

import (
	"fmt"
	"testing"
)

func TestClampSizeDecoratedMin(t *testing.T) {
	fw := NewFloatingWindow("cs-dmin", "CS", testWidget{}, 1, 1)
	ClampSize(fw)
	w, h := fw.Size()
	if w < 5 {
		t.Errorf("decorated min width = %d, want at least 5", w)
	}
	if h < 3 {
		t.Errorf("decorated min height = %d, want at least 3", h)
	}
}

func TestClampSizeDecoratedCustomMin(t *testing.T) {
	fw := NewFloatingWindow("cs-cmin", "CS", testWidget{}, 10, 10,
		WithMinSize(3, 2))
	ClampSize(fw)
	w, h := fw.Size()
	if w < 5 {
		t.Errorf("decorated min width with custom min = %d, want at least 5", w)
	}
	if h < 3 {
		t.Errorf("decorated min height with custom min = %d, want at least 3", h)
	}
}

func TestClampSizeUndecoratedMin(t *testing.T) {
	fw := NewFloatingWindow("cs-umin", "CS", testWidget{}, 0, 0,
		WithDecoration(false))
	ClampSize(fw)
	w, h := fw.Size()
	if w < 1 || h < 1 {
		t.Errorf("undecorated min size = (%d,%d), want at least (1,1)", w, h)
	}
}

func TestClampSizeMax(t *testing.T) {
	fw := NewFloatingWindow("cs-max", "CS", testWidget{}, 100, 100,
		WithMaxSize(30, 20))
	ClampSize(fw)
	w, h := fw.Size()
	if w != 30 {
		t.Errorf("width after max clamp = %d, want 30", w)
	}
	if h != 20 {
		t.Errorf("height after max clamp = %d, want 20", h)
	}
}

func TestClampSizeMaxZeroUnlimited(t *testing.T) {
	fw := NewFloatingWindow("cs-unlim", "CS", testWidget{}, 100, 100)
	ClampSize(fw)
	w, h := fw.Size()
	if w != 100 || h != 100 {
		t.Errorf("unlimited size should not clamp, got (%d,%d)", w, h)
	}
}

func TestClampSizeNoOpWhenWithinBounds(t *testing.T) {
	fw := NewFloatingWindow("cs-nop", "CS", testWidget{}, 20, 10)
	ClampSize(fw)
	w, h := fw.Size()
	if w != 20 || h != 10 {
		t.Errorf("ClampSize should not change valid size, got (%d,%d)", w, h)
	}
}

func TestClampPositionStaysOnScreen(t *testing.T) {
	fw := NewFloatingWindow("cp-on", "CP", testWidget{}, 20, 10)
	fw.SetPosition(5, 5)
	ClampPosition(fw, 80, 24)
	x, y := fw.Position()
	if x != 5 || y != 5 {
		t.Errorf("position when on screen = (%d,%d), want (5,5)", x, y)
	}
}

func TestClampPositionTooFarRight(t *testing.T) {
	fw := NewFloatingWindow("cp-right", "CP", testWidget{}, 20, 10)
	fw.SetPosition(100, 5)
	ClampPosition(fw, 80, 24)
	x, _ := fw.Position()
	if x > 70 {
		t.Errorf("x clamped = %d, want <= 70 (80-10)", x)
	}
}

func TestClampPositionTooFarDown(t *testing.T) {
	fw := NewFloatingWindow("cp-down", "CP", testWidget{}, 20, 10)
	fw.SetPosition(5, 100)
	ClampPosition(fw, 80, 24)
	_, y := fw.Position()
	if y > 21 {
		t.Errorf("y clamped = %d, want <= 21 (24-3)", y)
	}
}

func TestClampPositionTooFarLeft(t *testing.T) {
	fw := NewFloatingWindow("cp-left", "CP", testWidget{}, 20, 10)
	fw.SetPosition(-50, 5)
	ClampPosition(fw, 80, 24)
	x, _ := fw.Position()
	if x < -10 {
		t.Errorf("x clamped = %d, want >= -10 (-20+10)", x)
	}
}

func TestClampPositionTooFarUp(t *testing.T) {
	fw := NewFloatingWindow("cp-up", "CP", testWidget{}, 20, 10)
	fw.SetPosition(5, -50)
	ClampPosition(fw, 80, 24)
	_, y := fw.Position()
	if y < -7 {
		t.Errorf("y clamped = %d, want >= -7 (-10+3)", y)
	}
}

func TestClampPositionZeroScreen(t *testing.T) {
	fw := NewFloatingWindow("cp-zero", "CP", testWidget{}, 20, 10)
	fw.SetPosition(5, 5)
	ClampPosition(fw, 0, 0)
	x, y := fw.Position()
	if x != 5 || y != 5 {
		t.Errorf("ClampPosition modified position with zero screen, got (%d,%d)", x, y)
	}
}

func TestClampPositionWindowLargerThanScreen(t *testing.T) {
	fw := NewFloatingWindow("cp-large", "CP", testWidget{}, 200, 100)
	fw.SetPosition(0, 0)
	ClampPosition(fw, 80, 24)
	x, y := fw.Position()
	if x > 70 || y > 21 {
		t.Errorf("large window position = (%d,%d), want <= (70,21)", x, y)
	}
}

func TestCenterOnScreen(t *testing.T) {
	fw := NewFloatingWindow("center", "Center", testWidget{}, 20, 10)
	CenterOnScreen(fw, 80, 24)
	x, y := fw.Position()
	if x != 30 {
		t.Errorf("centered x = %d, want 30 ((80-20)/2)", x)
	}
	if y != 7 {
		t.Errorf("centered y = %d, want 7 ((24-10)/2)", y)
	}
}

func TestCenterOnScreenExactFit(t *testing.T) {
	fw := NewFloatingWindow("center-exact", "CE", testWidget{}, 80, 24)
	CenterOnScreen(fw, 80, 24)
	x, y := fw.Position()
	if x != 0 || y != 0 {
		t.Errorf("center when same size = (%d,%d), want (0,0)", x, y)
	}
}

func TestCascadePositions(t *testing.T) {
	windows := []*FloatingWindow{
		NewFloatingWindow("w0", "W0", testWidget{}, 20, 10),
		NewFloatingWindow("w1", "W1", testWidget{}, 20, 10),
		NewFloatingWindow("w2", "W2", testWidget{}, 20, 10),
	}
	CascadePositions(windows, 80, 24)

	x0, y0 := windows[0].Position()
	if x0 != 4 || y0 != 2 {
		t.Errorf("window[0] position = (%d,%d), want (4,2)", x0, y0)
	}

	x1, y1 := windows[1].Position()
	if x1 != 6 || y1 != 3 {
		t.Errorf("window[1] position = (%d,%d), want (6,3)", x1, y1)
	}

	x2, y2 := windows[2].Position()
	if x2 != 8 || y2 != 4 {
		t.Errorf("window[2] position = (%d,%d), want (8,4)", x2, y2)
	}
}

func TestCascadePositionsWraps(t *testing.T) {
	windows := make([]*FloatingWindow, 30)
	for i := range windows {
		windows[i] = NewFloatingWindow(
			fmt.Sprintf("w%d", i), "W", testWidget{}, 20, 10)
	}
	CascadePositions(windows, 80, 24)

	for _, fw := range windows {
		_, y := fw.Position()
		if y > 19 {
			t.Errorf("window %s y = %d, want <= 19 (24-5)", fw.ID, y)
		}
	}
}

func TestCascadePositionsEmpty(t *testing.T) {
	CascadePositions(nil, 80, 24)
	CascadePositions([]*FloatingWindow{}, 80, 24)
}
