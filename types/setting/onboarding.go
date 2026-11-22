package setting

// OnboardingSettings holds application choices, never workspace overrides.
// Configuration validity remains with the account and agent settings owners.
type Onboarding struct {
	IntroductionCompleted bool `json:"introduction_completed,omitempty"`
	AISetupReminderHidden bool `json:"ai_setup_reminder_hidden,omitempty"`
}
