package promptref

import (
	"fmt"
	"strings"
)

type Kind string

const (
	KindPastedText     Kind = "pasted_text"
	KindPastedTextFile Kind = "pasted_text_file"
	KindPastedImage    Kind = "pasted_image"
	KindSelectedFile   Kind = "selected_file"
)

type Reference struct {
	ID        string
	SessionID string
	Kind      Kind
	Label     string
	Title     string
	Mime      string
	Content   string
	FilePath  string
	Inline    bool
	CreatedAt int64
	DeletedAt int64
}

type CreateRequest struct {
	Kind     Kind
	Label    string
	Title    string
	Mime     string
	Content  string
	FilePath string
	Inline   bool
}

func (r Reference) Active() bool {
	return r.ID != "" && r.DeletedAt == 0
}

func (r Reference) DisplayName() string {
	if r.Label != "" {
		return r.Label
	}
	if r.Title != "" {
		return r.Title
	}
	if r.FilePath != "" {
		parts := strings.FieldsFunc(r.FilePath, func(ch rune) bool { return ch == '/' || ch == '\\' })
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return string(r.Kind)
}

func (r Reference) AgentText() string {
	name := r.DisplayName()
	switch r.Kind {
	case KindPastedText:
		return fmt.Sprintf("Reference %s (%s):\n%s", name, r.ID, r.Content)
	case KindPastedTextFile:
		return fmt.Sprintf("Reference %s (%s): pasted text saved at %s", name, r.ID, r.FilePath)
	case KindPastedImage:
		return fmt.Sprintf("Reference %s (%s): pasted image saved at %s", name, r.ID, r.FilePath)
	case KindSelectedFile:
		return fmt.Sprintf("Reference %s (%s): selected file at %s", name, r.ID, r.FilePath)
	default:
		if r.FilePath != "" {
			return fmt.Sprintf("Reference %s (%s): %s", name, r.ID, r.FilePath)
		}
		return fmt.Sprintf("Reference %s (%s):\n%s", name, r.ID, r.Content)
	}
}
