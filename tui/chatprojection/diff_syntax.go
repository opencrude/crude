package chatprojection

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
)

// diffSyntaxSyncBytes is the most diff text one row lexes on the caller's
// goroutine. Chroma lexes on the order of a kilobyte a millisecond (several
// times slower for Markdown and on Windows), and file edits render expanded on
// the UI goroutine during history materialization, so a small edit is colored
// at once while a larger one paints with its diff bands and receives its
// syntax colors from the background lexer within one activity phase.
const diffSyntaxSyncBytes = 1 << 10

// diffSyntaxCacheBytes bounds the retained background results.
const diffSyntaxCacheBytes = 16 << 20

type diffSyntaxJobKey struct {
	lexer string
	text  string
}

type diffSyntaxJob struct {
	key     diffSyntaxJobKey
	lexer   chroma.Lexer
	pending []string
}

// diffSyntax holds background lex results for whole diff files. Rows that
// found their file pending mark themselves frame-sensitive, so the renderer
// repaints them on the next activity phase and picks the result up here.
var diffSyntax struct {
	mu      sync.Mutex
	done    map[diffSyntaxJobKey][][]RowSpan
	order   []diffSyntaxJobKey
	bytes   int
	queued  map[diffSyntaxJobKey]bool
	queue   []diffSyntaxJob
	running bool
}

// prepareDiffSyntax fills cache with tokens for texts, lexing synchronously
// while the row's remaining budget covers them. It reports false when the
// file is being lexed in the background; its rows then use the fallback role
// colors (cache holds empty tokens so no row lexes on its own).
func prepareDiffSyntax(lexer chroma.Lexer, texts []string, cache map[diffSyntaxKey][]RowSpan, budget *int) bool {
	if lexer == nil || cache == nil {
		return true
	}
	pending := pendingDiffSegments(texts, cache)
	size := 0
	for _, text := range pending {
		size += len(text) + 1
	}
	if size <= *budget {
		*budget -= size
		for i, tokens := range lexDiffLines(lexer, pending) {
			cache[diffSyntaxKey{text: pending[i], role: tokensRole}] = tokens
		}
		return true
	}
	key := diffSyntaxJobKey{lexer: lexer.Config().Name, text: strings.Join(pending, "\n")}
	lines, ready := lookupDiffSyntax(key, lexer, pending)
	for i, text := range pending {
		var tokens []RowSpan
		if ready {
			tokens = lines[i]
		}
		cache[diffSyntaxKey{text: text, role: tokensRole}] = tokens
	}
	return ready
}

// lookupDiffSyntax returns a finished result or queues the lexing once.
func lookupDiffSyntax(key diffSyntaxJobKey, lexer chroma.Lexer, pending []string) ([][]RowSpan, bool) {
	diffSyntax.mu.Lock()
	defer diffSyntax.mu.Unlock()
	if lines, ok := diffSyntax.done[key]; ok {
		return lines, true
	}
	if diffSyntax.queued == nil {
		diffSyntax.queued = make(map[diffSyntaxJobKey]bool)
	}
	if !diffSyntax.queued[key] {
		diffSyntax.queued[key] = true
		diffSyntax.queue = append(diffSyntax.queue, diffSyntaxJob{key: key, lexer: lexer, pending: append([]string(nil), pending...)})
		if !diffSyntax.running {
			diffSyntax.running = true
			go runDiffSyntax()
		}
	}
	return nil, false
}

// runDiffSyntax drains the queue in arrival order and exits when it is empty.
// Lexing is pure computation over immutable inputs; nothing here touches a
// renderer.
func runDiffSyntax() {
	for {
		diffSyntax.mu.Lock()
		if len(diffSyntax.queue) == 0 {
			diffSyntax.running = false
			diffSyntax.mu.Unlock()
			return
		}
		job := diffSyntax.queue[0]
		diffSyntax.queue = diffSyntax.queue[1:]
		diffSyntax.mu.Unlock()

		lines := lexDiffLines(job.lexer, job.pending)

		diffSyntax.mu.Lock()
		delete(diffSyntax.queued, job.key)
		if diffSyntax.done == nil {
			diffSyntax.done = make(map[diffSyntaxJobKey][][]RowSpan)
		}
		diffSyntax.done[job.key] = lines
		diffSyntax.order = append(diffSyntax.order, job.key)
		// Tokens share the text's bytes; count the key and spans once.
		diffSyntax.bytes += 2 * len(job.key.text)
		for len(diffSyntax.order) > 1 && diffSyntax.bytes > diffSyntaxCacheBytes {
			oldest := diffSyntax.order[0]
			diffSyntax.order = diffSyntax.order[1:]
			diffSyntax.bytes -= 2 * len(oldest.text)
			delete(diffSyntax.done, oldest)
		}
		diffSyntax.mu.Unlock()
	}
}

// DiffSyntaxBusy reports whether background diff lexing is in progress, so a
// transcript keeps repainting until its rows have their colors.
func DiffSyntaxBusy() bool {
	diffSyntax.mu.Lock()
	defer diffSyntax.mu.Unlock()
	return diffSyntax.running
}
