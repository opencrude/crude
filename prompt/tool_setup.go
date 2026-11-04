package prompt

import (
	"fmt"
	"strings"
)

// ToolSetupItem is one tool the user chose to set up or to leave alone. Every
// field is data that Crude found; none of it is a decision about how to install.
type ToolSetupItem struct {
	// Languages are the language IDs the tool serves. They name the
	// lsp.languages.<id> settings.
	Languages []string
	Package   string
	Version   string
	Source    string
	// Command is the program Crude starts for the tool.
	Command string
	// Status is Crude's own text about why the tool is not ready.
	Status string
}

// ToolSetupTask is the work order that the setup screen sends to the
// workspace's Aeron. The user's switches decide which list a tool is in.
type ToolSetupTask struct {
	Project string
	Setup   []ToolSetupItem
	Skip    []ToolSetupItem
}

const toolSetupRules = `<rules>
<rule>Work only on the tools in set_up. Do not install, change or remove anything for the tools in leave_alone.</rule>
<rule>Crude starts each tool by its command. Make sure that command runs from the user's PATH or from Crude's tool folder. Prefer installs for the current user. If a step needs administrator rights, ask the user once.</rule>
<rule>Search the web and read the tool's official documentation to find the right installation and configuration for this computer and project. The listed package, version, source and command are discovery clues, not a required installation recipe. Choose a package manager, official release or other suitable method yourself.</rule>
<rule>After an install, run the tool's version command to check that it starts. Then make Crude use it with crude_control at workspace scope: if the command is not on the PATH, set lsp.languages.&lt;id&gt;.command (and arguments, if the tool needs them) to the installed program, and set lsp.languages.&lt;id&gt;.enabled to true.</rule>
<rule>Use the question tool only for a decision that you cannot make yourself. Ask once and give the options. Do not ask for confirmation of routine steps. Do not ask what you can find out by looking.</rule>
<rule>If a step fails, investigate and use another suitable method when that can help. Do not retry in a loop with the same failure. Report an unresolved failure once with its cause and the next step.</rule>
<rule>Keep your progress in your plan, not in questions. End with one short message that lists each tool as working or failed, with the reason for a failure.</rule>
</rules>`

// ToolSetupMessage renders the work order for Aeron. The setup screen builds
// the data; the wording of the instructions lives only here.
func ToolSetupMessage(task ToolSetupTask) string {
	var b strings.Builder
	b.WriteString("<tool_setup_task>\n")
	fmt.Fprintf(&b, "<project>%s</project>\n", XMLText(task.Project))
	b.WriteString("<goal>Make code intelligence work for this project. Install the language tools in set_up so that Crude finds them, then check that they work.</goal>\n")
	writeToolSetupList(&b, "set_up", task.Setup)
	writeToolSetupList(&b, "leave_alone", task.Skip)
	b.WriteString(toolSetupRules)
	b.WriteString("\n</tool_setup_task>")
	return b.String()
}

func writeToolSetupList(b *strings.Builder, name string, items []ToolSetupItem) {
	if len(items) == 0 {
		fmt.Fprintf(b, "<%s/>\n", name)
		return
	}
	fmt.Fprintf(b, "<%s>\n", name)
	for _, item := range items {
		fmt.Fprintf(b, `<tool language_ids="%s" package="%s" version="%s" command="%s" source="%s">%s</tool>`+"\n",
			XMLText(strings.Join(item.Languages, ", ")), XMLText(item.Package), XMLText(item.Version),
			XMLText(item.Command), XMLText(item.Source), XMLText(item.Status))
	}
	fmt.Fprintf(b, "</%s>\n", name)
}
