package workspace

import "fmt"

// TerminalWork names a terminal whose program a core stop would interrupt.
type TerminalWork struct {
	ID, Name, Program string
}

func (w TerminalWork) Error() string {
	if w.Program == "" {
		return fmt.Sprintf("%s is running a program", w.Name)
	}
	return fmt.Sprintf("%s is running %s", w.Name, w.Program)
}

// RunningPrograms lists live terminals that run more than an idle shell. An
// interactive shell waiting at its prompt is not work: closing it loses only
// its screen, as when an IDE exits. A terminal started with its own command,
// or a shell with a foreground or background job, is work. A terminal whose
// state cannot be read counts as running (fail closed).
func (m *TerminalManager) RunningPrograms() []TerminalWork {
	if m == nil {
		return nil
	}
	type candidate struct {
		info        TerminalInfo
		io          terminalIO
		interactive bool
		program     string
	}
	m.mu.Lock()
	candidates := make([]candidate, 0, len(m.order))
	for _, id := range m.order {
		if s := m.items[id]; s != nil && s.alive {
			candidates = append(candidates, candidate{s.info(), s.io, s.interactive, s.program})
		}
	}
	m.mu.Unlock()
	var out []TerminalWork
	for _, c := range candidates {
		work := TerminalWork{ID: c.info.ID, Name: c.info.DisplayName(), Program: c.program}
		if !c.interactive {
			out = append(out, work)
			continue
		}
		program, busy, err := terminalProgram(c.io)
		if err != nil {
			work.Program = ""
			out = append(out, work)
			continue
		}
		if busy {
			work.Program = program
			out = append(out, work)
		}
	}
	return out
}
