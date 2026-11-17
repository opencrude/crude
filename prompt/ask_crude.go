package prompt

// Ask Crude's prepared prompts are the user's own message: plain requests
// that name the attached code by its chip label. They carry no policy.

// AskExplain asks the agent to explain attached code.
func AskExplain(label string) string {
	return "Explain " + label + "."
}

// AskFix asks the agent to fix one problem that the editor reports in
// attached code.
func AskFix(label, problem string) string {
	return "Fix this problem in " + label + ": " + problem
}
