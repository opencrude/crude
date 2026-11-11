package workspace

import (
	"context"
	"crude/document"
	"crude/indentation"
)

func (s *DocumentService) EditorConfig(ctx context.Context, owner document.Owner, id string) (string, indentation.Choices, error) {
	s.mu.Lock()
	d, err := s.owned(owner, id)
	path := ""
	if err == nil {
		path = d.snapshot.Path
	}
	s.mu.Unlock()
	if err != nil {
		return "", indentation.Choices{}, err
	}
	choices, err := indentation.ReadProject(ctx, path)
	if err != nil {
		return path, indentation.Choices{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err = s.owned(owner, id)
	if err == nil && d.snapshot.Path != path {
		err = document.ErrRevision
	}
	if err == nil {
		err = ctx.Err()
	}
	return path, choices, err
}
