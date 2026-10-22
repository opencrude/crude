package rendering

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// lexerIndex memoizes lexer selection. lexers.Match globs a name against
// every registered pattern (about a thousand); transcripts with many patched
// files paid that per distinct name on the UI goroutine. Most names are
// decided by their extension alone, so results are shared per extension;
// names that hit one of the few name-specific patterns ("Makefile",
// "go.mod", "*.d.ts", "Dockerfile.*") or a backup suffix keep exact
// per-name matching.
var lexerIndex struct {
	sync.Mutex
	once    sync.Once
	special []string
	byName  map[string]chroma.Lexer
	byExt   map[string]chroma.Lexer
}

const lexerCacheLimit = 4096

var extensionGlob = regexp.MustCompile(`^\*\.[A-Za-z0-9_+-]+$`)

// backupSuffixes mirrors chroma's ignored suffixes: their names are matched
// with and without the suffix, so they never share an extension entry.
var backupSuffixes = []string{"~", ".bak", ".old", ".orig", ".dpkg-dist", ".dpkg-old", ".ucf-dist", ".ucf-new", ".ucf-old", ".rpmnew", ".rpmorig", ".rpmsave", ".in"}

func loadSpecialGlobs() {
	for _, lexer := range lexers.GlobalLexerRegistry.Lexers {
		config := lexer.Config()
		for _, globs := range [][]string{config.Filenames, config.AliasFilenames} {
			for _, glob := range globs {
				if !extensionGlob.MatchString(glob) {
					lexerIndex.special = append(lexerIndex.special, glob)
				}
			}
		}
	}
}

func matchesSpecial(name string) bool {
	for _, glob := range lexerIndex.special {
		if ok, _ := filepath.Match(glob, name); ok {
			return true
		}
	}
	return false
}

// MatchLexer returns the Chroma lexer for path's file name, or nil when no
// lexer matches; the result equals lexers.Match. Lexers are shared,
// immutable registry entries.
func MatchLexer(path string) chroma.Lexer {
	name := filepath.Base(path)
	lexerIndex.once.Do(loadSpecialGlobs)
	lexerIndex.Lock()
	defer lexerIndex.Unlock()
	if lexer, ok := lexerIndex.byName[name]; ok {
		return lexer
	}
	ext := filepath.Ext(name)
	byExtension := ext != "" && !hasBackupSuffix(name) && !matchesSpecial(name)
	if byExtension {
		if lexer, ok := lexerIndex.byExt[ext]; ok {
			return lexer
		}
	}
	lexer := lexers.Match(name)
	if lexerIndex.byName == nil || len(lexerIndex.byName) >= lexerCacheLimit {
		lexerIndex.byName = make(map[string]chroma.Lexer)
	}
	lexerIndex.byName[name] = lexer
	if byExtension {
		if lexerIndex.byExt == nil {
			lexerIndex.byExt = make(map[string]chroma.Lexer)
		}
		lexerIndex.byExt[ext] = lexer
	}
	return lexer
}

func hasBackupSuffix(name string) bool {
	for _, suffix := range backupSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
