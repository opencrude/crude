package unidiff

import (
	"strings"
	"sync"
	"time"

	"crude/internal/logger"

	"github.com/aymanbagabas/go-udiff"
)

var (
	diffLogger   *logger.Logger
	diffLoggerMu sync.RWMutex
)

func SetLogger(lg *logger.Logger) {
	diffLoggerMu.Lock()
	defer diffLoggerMu.Unlock()
	diffLogger = lg
}

func diffLogIf(fn func(lg *logger.Logger)) {
	diffLoggerMu.RLock()
	lg := diffLogger
	diffLoggerMu.RUnlock()
	if lg != nil {
		fn(lg)
	}
}

func GenerateDiff(beforeContent, afterContent, fileName string) (string, int, int) {
	start := time.Now()
	fileName = strings.TrimPrefix(fileName, "/")

	edits := udiff.Lines(beforeContent, afterContent)
	unified := udiff.Unified("a/"+fileName, "b/"+fileName, beforeContent, afterContent)

	// Count inserted and deleted lines from the structured edits rather than
	// re-parsing the rendered diff. A rendered added line whose text begins
	// with "++" is written as "+++<text>", which the previous prefix check
	// mistook for the "+++" file header and skipped, so a real change was
	// reported as zero additions (and symmetrically for "--").
	additions, removals := countLineEdits(beforeContent, edits)

	diffLogIf(func(lg *logger.Logger) {
		lg.Debug("DIFF", "generate", "file", fileName, "additions", additions, "removals", removals, "diff_len", len(unified), "latency_ms", time.Since(start).Milliseconds())
	})
	return unified, additions, removals
}

// IsEmptyDiff returns true if the unified diff contains no actual changes.
func IsEmptyDiff(unified string) bool {
	// A non-empty unified diff always begins with the two file headers
	// ("--- ..." and "+++ ..."). After them, every line is either a hunk
	// header ("@@ ... @@"), a "\ No newline at end of file" marker, an added
	// line ("+..."), a removed line ("-...") or a context line (" ...").
	// Skip the two leading headers, then look for any line whose first byte
	// is '+' or '-'. Counting by the first byte avoids mistaking an added
	// line whose text begins with "++" (rendered as "+++<text>") for the
	// "+++" header, and the symmetric "--" case.
	seenOld, seenNew := false, false
	for line := range strings.SplitSeq(unified, "\n") {
		if !seenOld {
			if strings.HasPrefix(line, "--- ") {
				seenOld = true
			}
			continue
		}
		if !seenNew {
			if strings.HasPrefix(line, "+++ ") {
				seenNew = true
			}
			continue
		}
		if line == "" {
			continue
		}
		switch line[0] {
		case '+':
			return false
		case '-':
			return false
		}
	}
	return true
}

// countLineEdits counts the inserted and removed lines represented by a set
// of udiff edits, matching the lines the renderer would mark '+' and '-'. It
// uses the structured edits so the counts stay correct for added lines that
// begin with "++" or removed lines that begin with "--", which the renderer
// emits as "+++<text>" / "---<text>" and which a prefix check on the rendered
// diff mistakes for the file headers.
func countLineEdits(content string, edits []udiff.Edit) (additions, removals int) {
	for _, edit := range edits {
		removals += countWholeLines(content[edit.Start:edit.End])
		additions += countWholeLines(edit.New)
	}
	return additions, removals
}

// countWholeLines counts the whole lines a diff renderer would mark '+' or
// '-', counting a final unterminated line the way go-udiff does.
func countWholeLines(segment string) int {
	if segment == "" {
		return 0
	}
	n := strings.Count(segment, "\n")
	if !strings.HasSuffix(segment, "\n") {
		n++ // an unterminated final line is still a whole line for the diff
	}
	return n
}
