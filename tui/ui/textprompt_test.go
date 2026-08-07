package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
