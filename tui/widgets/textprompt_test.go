package widgets

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"crude/tui/theme"
)

func TestTextPromptLines_Empty(t *testing.T) {
	th := theme.DefaultDark()
	st := &TextPromptState{
		Title:  "New file",
		Prompt: "in /tmp",
		Kind:   TextPromptNewFile,
	}
	lines := TextPromptLines(th, st)
	assert.NotEmpty(t, lines)
	// Chrome wraps: top border + prompt + input + empty + hint +
	// bottom border = 6.
	assert.Len(t, lines, 6)
}

func TestTextPromptLines_RenamePrefill(t *testing.T) {
	th := theme.DefaultDark()
	st := &TextPromptState{
		Title:  "Rename",
		Prompt: "/home/user/foo.txt",
		Kind:   TextPromptRename,
		Value:  "foo.txt",
		Cursor: 7,
	}
	lines := TextPromptLines(th, st)
	assert.Len(t, lines, 6)
	// The input field row (line 2, after top border + prompt) should
	// contain the value.
	plain := stripForVisible(lines[2])
	assert.Contains(t, plain, "foo.txt")
}

func TestTextPromptInputField_EmptyShowsPlaceholder(t *testing.T) {
	th := theme.DefaultDark()
	out := PopupInputField(th, "", "", "name", 0, true)
	assert.Contains(t, stripForVisible(out), "name")
}

func TestTextPromptInputField_ClampsCursor(t *testing.T) {
	th := theme.DefaultDark()
	// Cursor past end is clamped to len(value).
	out := PopupInputField(th, "", "hi", "", 99, true)
	assert.Contains(t, stripForVisible(out), "hi")
	// Negative cursor is clamped to 0.
	out = PopupInputField(th, "", "hi", "", -5, true)
	assert.Contains(t, stripForVisible(out), "hi")
}

func TestTextPromptPopupWidth_HonorsMinAndMax(t *testing.T) {
	st := &TextPromptState{Title: "X"}
	w := TextPromptPopupWidth(st)
	assert.GreaterOrEqual(t, w, 50)
	st.Title = "this is a very very very long title that would be wider than the max"
	w = TextPromptPopupWidth(st)
	assert.LessOrEqual(t, w, 70)
}

func TestTextPromptPopupHeight(t *testing.T) {
	st := &TextPromptState{}
	assert.Equal(t, 7, TextPromptPopupHeight(st))
}

func TestKindIcon(t *testing.T) {
	assert.Equal(t, "✎", kindIcon(TextPromptNewFile))
	assert.Equal(t, "▤", kindIcon(TextPromptNewFolder))
	assert.Equal(t, "↪", kindIcon(TextPromptRename))
	assert.Equal(t, "•", kindIcon(TextPromptGeneric))
}
