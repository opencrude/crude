package settings

import (
	"crude/tui/ui"
	"crude/types"
)

const (
	fieldExecutionWarnings = "execution-hygiene-warnings"
	fieldExecutionCount    = "execution-hygiene-count"
	fieldExecutionAge      = "execution-hygiene-age"
	fieldExecutionStorage  = "execution-hygiene-storage"
)

func executionHygienePath(id string) string {
	leaf := map[string]string{fieldExecutionWarnings: "warnings", fieldExecutionCount: "retained_count", fieldExecutionAge: "age_hours", fieldExecutionStorage: "storage_mb"}[id]
	if leaf == "" {
		return ""
	}
	return "agents.execution_hygiene." + leaf
}

func (m *Model) executionHygieneFields() []*ui.FieldState {
	s := m.agents.ExecutionHygiene
	enabled, _, _, _ := s.Values()
	return []*ui.FieldState{
		ui.SeparatorFieldState("execution-hygiene", "Worktree hygiene"),
		ui.BoolFieldState(fieldExecutionWarnings, "Remind parent to clean up", enabled,
			"Remind parents to review and clean up retained work; never blocks launches."),
		optionalNumberField(fieldExecutionCount, "Warn at retained worktrees", s.RetainedCount, "worktrees",
			"Blank is 16; 0 disables this reminder. No automatic deletion.", 0, 1000000),
		optionalNumberField(fieldExecutionAge, "Warn after", s.AgeHours, "hours",
			"Blank is 24; 0 disables age reminders for retained worktrees.", 0, 1000000),
		optionalNumberField(fieldExecutionStorage, "Warn at snapshot storage", s.StorageMB, "MB",
			"Blank is 1024; 0 disables snapshot-storage reminders, not a disk quota.", 0, 1000000),
	}
}

func executionHygieneFromForm(form *ui.FormModel, before types.ExecutionHygieneSettings) types.ExecutionHygieneSettings {
	if field := form.Field(fieldExecutionWarnings); field != nil && !field.Value.Equal(field.Initial) {
		n := ui.BoolFieldValue(field)
		before.Warnings = &n
	}
	for id, dst := range map[string]**int{fieldExecutionCount: &before.RetainedCount, fieldExecutionAge: &before.AgeHours, fieldExecutionStorage: &before.StorageMB} {
		if field := form.Field(id); field != nil {
			*dst = parseIntPtr(field.Value.String())
		}
	}
	return before
}
