package rendering

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// lexerIndex memoizes lexer selection. lexers.Match globs a name against
// every registered pattern, with and without each backup suffix (about 2 ms
// a name). A dense transcript opened with a cold cache paid that for every
// distinct file name on the UI goroutine: names without an extension
// ("Makefile", a folder) first, then each new extension.
//
// The index replays chroma's own walk over only the patterns that can match a
// name. A "*.ext" pattern can match only a name whose extension, with or
// without a backup suffix, is ext; the other patterns (81 in chroma 2.20) are
// always tried. Candidates keep chroma's registry and pattern order and pass
// chroma's own test, so the priority sort picks what lexers.Match picks.
var lexerIndex struct {
	sync.Mutex
	once sync.Once
	// phases are chroma's two matching passes: file names, then alias file
	// names. Complete and read-only after once.
	phases [2]lexerPhase
	byName map[string]chroma.Lexer
}

// lexerPhase holds one pass's patterns: "*.ext" patterns by extension and
// every other pattern, each in chroma's walk order.
type lexerPhase struct {
	byExt   map[string][]lexerGlob
	special []lexerGlob
}

// lexerGlob is one registered file-name pattern. order is its position in
// chroma's walk: registry order, then the lexer's pattern order.
type lexerGlob struct {
	order int
	glob  string
	lexer chroma.Lexer
}

const lexerCacheLimit = 4096

var extensionGlob = regexp.MustCompile(`^\*\.[A-Za-z0-9_+-]+$`)

// backupSuffixes mirrors chroma's ignored suffixes: a pattern also matches a
// name that adds one of them.
var backupSuffixes = []string{"~", ".bak", ".old", ".orig", ".dpkg-dist", ".dpkg-old", ".ucf-dist", ".ucf-new", ".ucf-old", ".rpmnew", ".rpmorig", ".rpmsave", ".in"}

func loadLexerIndex() {
	order := 0
	for kind := range lexerIndex.phases {
		phase := &lexerIndex.phases[kind]
		phase.byExt = make(map[string][]lexerGlob)
		for _, lexer := range lexers.GlobalLexerRegistry.Lexers {
			config := lexer.Config()
			globs := config.Filenames
			if kind == 1 {
				globs = config.AliasFilenames
			}
			for _, glob := range globs {
				entry := lexerGlob{order: order, glob: glob, lexer: lexer}
				order++
				if extensionGlob.MatchString(glob) {
					phase.byExt[glob[1:]] = append(phase.byExt[glob[1:]], entry)
				} else {
					phase.special = append(phase.special, entry)
				}
			}
		}
	}
}

// MatchLexer returns the Chroma lexer for path's file name, or nil when no
// lexer matches; the result equals lexers.Match. Lexers are shared,
// immutable registry entries.
func MatchLexer(path string) chroma.Lexer {
	name := filepath.Base(path)
	lexerIndex.once.Do(loadLexerIndex)
	lexerIndex.Lock()
	lexer, ok := lexerIndex.byName[name]
	lexerIndex.Unlock()
	if ok {
		return lexer
	}
	lexer = matchIndexed(name)
	lexerIndex.Lock()
	defer lexerIndex.Unlock()
	if lexerIndex.byName == nil || len(lexerIndex.byName) >= lexerCacheLimit {
		lexerIndex.byName = make(map[string]chroma.Lexer)
	}
	lexerIndex.byName[name] = lexer
	return lexer
}

// matchIndexed is chroma's Match restricted to the patterns that can match
// name: the first pass with a match decides, by chroma's priority sort over
// the matches in walk order.
func matchIndexed(name string) chroma.Lexer {
	for _, phase := range lexerIndex.phases {
		candidates := append([]lexerGlob(nil), phase.special...)
		candidates = append(candidates, phase.byExt[filepath.Ext(name)]...)
		for _, suffix := range backupSuffixes {
			if stem, ok := strings.CutSuffix(name, suffix); ok {
				candidates = append(candidates, phase.byExt[filepath.Ext(stem)]...)
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].order < candidates[j].order })
		var matched chroma.PrioritisedLexers
		for i, candidate := range candidates {
			if i > 0 && candidate.order == candidates[i-1].order {
				continue // reached through the name and a backup suffix
			}
			if globMatches(candidate.glob, name) {
				matched = append(matched, candidate.lexer)
			}
		}
		if len(matched) > 0 {
			sort.Sort(matched)
			return matched[0]
		}
	}
	return nil
}

// globMatches is chroma's per-pattern test: the pattern alone or followed by
// a backup suffix. A suffix is literal text, so a name can match the longer
// pattern only when it ends with that suffix.
func globMatches(glob, name string) bool {
	if ok, _ := filepath.Match(glob, name); ok {
		return true
	}
	for _, suffix := range backupSuffixes {
		if strings.HasSuffix(name, suffix) {
			if ok, _ := filepath.Match(glob+suffix, name); ok {
				return true
			}
		}
	}
	return false
}
