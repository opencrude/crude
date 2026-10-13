package capabilitypicker

import "reflect"

// PreviewReady observes the settled preview without starting I/O or changing
// drafts. Apply has already rejected stale selections and prompt arguments.
func (s *Screen) PreviewReady() bool {
	if s.busy || s.page != "detail" || s.preview == "" || !reflect.DeepEqual(s.selected, s.fetchSelection) {
		return false
	}
	arguments := make(map[string]string)
	for _, argument := range s.selected.Prompt.Arguments {
		field := s.model.Field("arg/" + argument.Name)
		if field == nil {
			return false
		}
		arguments[argument.Name] = field.Value.String()
	}
	return reflect.DeepEqual(arguments, s.fetchArguments)
}

func (s *Screen) ControlEditGeneration() uint64 { return s.model.EditGeneration() }
