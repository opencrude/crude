// Package textindex keeps an in-memory text index of one workspace. The core
// builds it in the background, the file watch keeps it current one file at a
// time, and two readers use it:
//
//   - Find in Files searches memory instead of reading every file again
//     (Files returns the same files, in the same order, as the disk walk);
//   - Aeron suggestions retrieve related code (Related ranks 40-line chunks
//     by BM25 over identifiers).
//
// It is language-neutral: tokens are identifiers, never language keywords.
// Memory is bounded; a file that does not fit keeps only its path, and its
// readers fall back to the disk for it.
package textindex

import (
	"context"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"crude/fileio"
	"crude/third_party/pathwalk"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

const (
	// DefaultMemoryLimit bounds the text that the index keeps.
	DefaultMemoryLimit = 64 << 20
	// MaxIndexedFileBytes is the largest file whose text is kept and ranked.
	MaxIndexedFileBytes = 1 << 20
	chunkLines          = 40
	minTokenLength      = 3
	maxQueryTokens      = 24
	bm25K1              = 1.2
	bm25B               = 0.75
)

// Snippet is one ranked chunk of a file.
type Snippet struct {
	Path  string // absolute
	Rel   string // relative, forward slashes
	Line  int    // 1-based first line
	Text  string
	Score float64
}

// Stats describes the index for diagnostics and the performance dashboard.
type Stats struct {
	Ready      bool
	Files      int
	TextBytes  int64
	Chunks     int
	Tokens     int
	Incomplete bool
	BuildTime  time.Duration
}

type file struct {
	path, rel  string
	text       []byte
	large      bool
	binary     bool
	unreadable bool
	ignored    bool // in an ignore file: searchable, never ranked
	chunks     []uint32
}

type chunk struct {
	file   *file
	line   int // 0-based first line
	start  int // byte offsets into file.text
	end    int
	length int // token count
	terms  []string
}

// Index is safe for concurrent use. Readers never wait for a build.
type Index struct {
	root  string
	limit int64

	mu         sync.RWMutex
	files      map[string]*file // by absolute path
	order      []*file          // walk order, rebuilt lazily
	orderDirty bool
	chunks     map[uint32]*chunk
	postings   map[string]map[uint32]uint16
	totalLen   int64
	nextChunk  uint32
	textBytes  int64
	incomplete bool
	ignore     gitignore.Matcher
	ready      atomic.Bool
	buildTime  time.Duration

	updates chan string
	cancel  context.CancelFunc
	done    chan struct{}
}

// New returns an empty index for root. Start builds it.
func New(root string, memoryLimit int64) *Index {
	if memoryLimit <= 0 {
		memoryLimit = DefaultMemoryLimit
	}
	return &Index{root: filepath.Clean(root), limit: memoryLimit, files: map[string]*file{},
		chunks: map[uint32]*chunk{}, postings: map[string]map[uint32]uint16{}, updates: make(chan string, 1024)}
}

// Start builds the index on its own worker and then applies file changes in
// order. Close cancels and joins it.
func (x *Index) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	x.cancel, x.done = cancel, make(chan struct{})
	go func() {
		defer close(x.done)
		began := time.Now()
		if x.build(ctx) != nil {
			return
		}
		x.mu.Lock()
		x.buildTime = time.Since(began)
		x.mu.Unlock()
		x.ready.Store(true)
		for {
			select {
			case <-ctx.Done():
				return
			case path := <-x.updates:
				x.refresh(path)
			}
		}
	}()
}

// Close stops the worker. The index stays readable but no longer changes.
func (x *Index) Close() {
	if x.cancel != nil {
		x.cancel()
		<-x.done
	}
	x.ready.Store(false)
}

// Ready reports a finished build. Before it, readers use the disk.
func (x *Index) Ready() bool { return x != nil && x.ready.Load() }

// Changed queues one changed, created or removed path. It never blocks; a
// full queue marks the index incomplete so readers use the disk.
func (x *Index) Changed(path string) {
	if x == nil {
		return
	}
	select {
	case x.updates <- filepath.Clean(path):
	default:
		x.mu.Lock()
		x.incomplete = true
		x.mu.Unlock()
	}
}

// Complete reports whether every searchable file's text is in memory or
// marked for a disk read, so Find in Files can trust Files.
func (x *Index) Complete() bool {
	if !x.Ready() {
		return false
	}
	x.mu.RLock()
	defer x.mu.RUnlock()
	return !x.incomplete
}

// Stats returns a snapshot for diagnostics.
func (x *Index) Stats() Stats {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return Stats{Ready: x.ready.Load(), Files: len(x.files), TextBytes: x.textBytes, Chunks: len(x.chunks),
		Tokens: len(x.postings), Incomplete: x.incomplete, BuildTime: x.buildTime}
}

func (x *Index) build(ctx context.Context) error {
	type found struct {
		path string
		size int64
	}
	var mu sync.Mutex
	var paths []found
	var ignores []gitignore.Pattern
	conf := &pathwalk.Config{NumWorkers: min(8, pathwalk.DefaultNumWorkers())}
	err := pathwalk.WalkContext(ctx, conf, x.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != x.root && fileio.ShouldSkipProjectDir(entry.Name(), false) {
				return fs.SkipDir
			}
			if patterns := readIgnore(x.root, path); len(patterns) > 0 {
				mu.Lock()
				ignores = append(ignores, patterns...)
				mu.Unlock()
			}
			return nil
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		info, err := entry.Info()
		size := int64(fileio.MaxProjectSearchFileSize + 1)
		if err == nil {
			size = info.Size()
		}
		mu.Lock()
		paths = append(paths, found{path, size})
		mu.Unlock()
		return nil
	})
	if err != nil {
		return err
	}
	x.mu.Lock()
	x.ignore = gitignore.NewMatcher(ignores)
	x.mu.Unlock()
	for _, f := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		x.load(f.path, f.size)
	}
	return nil
}

func readIgnore(root, dir string) []gitignore.Pattern {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	var domain []string
	if rel, err := filepath.Rel(root, dir); err == nil && rel != "." {
		domain = strings.Split(filepath.ToSlash(rel), "/")
	}
	var out []gitignore.Pattern
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" {
			out = append(out, gitignore.ParsePattern(line, domain))
		}
	}
	return out
}

// searchable mirrors the Find in Files walk: no skipped directory and no
// hidden file anywhere on the path.
func (x *Index) searchable(path string) (string, bool) {
	rel, err := filepath.Rel(x.root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, dir := range parts[:len(parts)-1] {
		if fileio.ShouldSkipProjectDir(dir, false) {
			return "", false
		}
	}
	if strings.HasPrefix(parts[len(parts)-1], ".") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (x *Index) refresh(path string) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil && info.IsDir() {
			x.refreshDir(path)
			return
		}
		x.mu.Lock()
		x.removeLocked(path)
		// A removed directory takes its files with it.
		prefix := path + string(filepath.Separator)
		for p := range x.files {
			if strings.HasPrefix(p, prefix) {
				x.removeLocked(p)
			}
		}
		x.mu.Unlock()
		return
	}
	if filepath.Base(path) == ".gitignore" {
		x.mu.Lock()
		x.incomplete = true // ignore rules changed; rank conservatively until rebuilt
		x.mu.Unlock()
	}
	x.load(path, info.Size())
}

func (x *Index) refreshDir(dir string) {
	if _, ok := x.searchable(filepath.Join(dir, "x")); !ok {
		return
	}
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != dir && fileio.ShouldSkipProjectDir(entry.Name(), false) {
				return fs.SkipDir
			}
			return nil
		}
		if info, err := entry.Info(); err == nil && entry.Type().IsRegular() {
			x.load(path, info.Size())
		}
		return nil
	})
}

func (x *Index) load(path string, size int64) {
	rel, ok := x.searchable(path)
	if !ok {
		return
	}
	f := &file{path: path, rel: rel}
	if size > fileio.MaxProjectSearchFileSize {
		f.large = true
	} else {
		f.text, f.binary, f.unreadable = fileio.ReadProjectFile(path)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.removeLocked(path)
	if f.text != nil && x.textBytes+int64(len(f.text)) > x.limit {
		// Keep the path: Find in Files reads this file from disk.
		f.text = nil
		x.incomplete = true
	}
	x.files[path] = f
	x.orderDirty = true
	x.textBytes += int64(len(f.text))
	if x.ignore != nil {
		f.ignored = x.ignore.Match(strings.Split(rel, "/"), false)
	}
	if f.text != nil && !f.ignored && len(f.text) <= MaxIndexedFileBytes {
		x.chunkLocked(f)
	}
}

func (x *Index) removeLocked(path string) {
	f := x.files[path]
	if f == nil {
		return
	}
	for _, id := range f.chunks {
		c := x.chunks[id]
		for _, term := range c.terms {
			if p := x.postings[term]; p != nil {
				delete(p, id)
				if len(p) == 0 {
					delete(x.postings, term)
				}
			}
		}
		x.totalLen -= int64(c.length)
		delete(x.chunks, id)
	}
	x.textBytes -= int64(len(f.text))
	delete(x.files, path)
	x.orderDirty = true
}

func (x *Index) chunkLocked(f *file) {
	text := f.text
	line, start := 0, 0
	for start < len(text) {
		end, lines := start, 0
		for end < len(text) && lines < chunkLines {
			next := indexByte(text[end:], '\n')
			if next < 0 {
				end = len(text)
				lines++
				break
			}
			end += next + 1
			lines++
		}
		counts := map[string]uint16{}
		length := 0
		forEachToken(text[start:end], func(token string) {
			if counts[token] < math.MaxUint16 {
				counts[token]++
			}
			length++
		})
		if length > 0 {
			id := x.nextChunk
			x.nextChunk++
			c := &chunk{file: f, line: line, start: start, end: end, length: length, terms: make([]string, 0, len(counts))}
			for term, n := range counts {
				p := x.postings[term]
				if p == nil {
					p = map[uint32]uint16{}
					x.postings[term] = p
				}
				p[id] = n
				c.terms = append(c.terms, term)
			}
			x.chunks[id] = c
			f.chunks = append(f.chunks, id)
			x.totalLen += int64(length)
		}
		line += lines
		start = end
	}
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// forEachToken yields lower-cased ASCII identifiers of at least three bytes.
// Non-ASCII letters end a token; that keeps the tokenizer language-neutral
// and allocation-light.
func forEachToken(text []byte, yield func(string)) {
	start := -1
	flush := func(end int) {
		if start >= 0 && end-start >= minTokenLength && (text[start] < '0' || text[start] > '9') {
			yield(strings.ToLower(string(text[start:end])))
		}
		start = -1
	}
	for i, c := range text {
		ident := c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if ident {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
	}
	flush(len(text))
}

// Tokens returns the distinct identifier tokens of text, in first-seen order.
func Tokens(text string) []string {
	seen := map[string]bool{}
	var out []string
	forEachToken([]byte(text), func(token string) {
		if !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
	})
	return out
}

// Files returns every searchable file in the order of the Find in Files walk.
// Text is shared and must not be modified.
func (x *Index) Files() []fileio.ProjectFile {
	x.mu.Lock()
	if x.orderDirty || x.order == nil {
		x.order = x.order[:0]
		for _, f := range x.files {
			x.order = append(x.order, f)
		}
		slices.SortFunc(x.order, func(a, b *file) int { return compareWalkOrder(a.rel, b.rel) })
		x.orderDirty = false
	}
	out := make([]fileio.ProjectFile, len(x.order))
	for i, f := range x.order {
		out[i] = fileio.ProjectFile{Path: f.path, Rel: f.rel, Text: f.text, Large: f.large, Binary: f.binary, Unreadable: f.unreadable}
	}
	x.mu.Unlock()
	return out
}

// compareWalkOrder orders slash paths as filepath.WalkDir visits them: entries
// of one directory by name, a directory's contents right after its name.
func compareWalkOrder(a, b string) int {
	for {
		ai, bi := strings.IndexByte(a, '/'), strings.IndexByte(b, '/')
		an, bn := a, b
		if ai >= 0 {
			an = a[:ai]
		}
		if bi >= 0 {
			bn = b[:bi]
		}
		if c := strings.Compare(an, bn); c != 0 {
			return c
		}
		if ai < 0 || bi < 0 {
			return ai - bi
		}
		a, b = a[ai+1:], b[bi+1:]
	}
}

// Related ranks chunks against the identifiers in query and returns up to
// limit snippets, skipping chunks for which skip returns true. It never waits
// for a build: before the index is ready it returns nothing.
func (x *Index) Related(query string, limit int, skip func(path string, line int) bool) []Snippet {
	if !x.Ready() || limit <= 0 {
		return nil
	}
	tokens := Tokens(query)
	x.mu.RLock()
	defer x.mu.RUnlock()
	if x.incomplete {
		return nil
	}
	n := float64(len(x.chunks))
	if n == 0 {
		return nil
	}
	avg := float64(x.totalLen) / n
	type weighted struct {
		term string
		idf  float64
	}
	terms := make([]weighted, 0, len(tokens))
	for _, t := range tokens {
		df := float64(len(x.postings[t]))
		if df == 0 || n >= 20 && df > n/4 {
			continue // absent, or too common to tell code apart
		}
		terms = append(terms, weighted{t, math.Log(1 + (n-df+0.5)/(df+0.5))})
	}
	slices.SortFunc(terms, func(a, b weighted) int {
		if a.idf > b.idf {
			return -1
		}
		if a.idf < b.idf {
			return 1
		}
		return strings.Compare(a.term, b.term)
	})
	terms = terms[:min(len(terms), maxQueryTokens)]
	scores := map[uint32]float64{}
	for _, t := range terms {
		for id, tf := range x.postings[t.term] {
			c := x.chunks[id]
			f := float64(tf)
			scores[id] += t.idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(c.length)/avg))
		}
	}
	ids := make([]uint32, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uint32) int {
		if scores[a] > scores[b] {
			return -1
		}
		if scores[a] < scores[b] {
			return 1
		}
		return int(a) - int(b)
	})
	var out []Snippet
	for _, id := range ids {
		if len(out) >= limit {
			break
		}
		c := x.chunks[id]
		if skip != nil && skip(c.file.path, c.line+1) {
			continue
		}
		text := c.file.text[c.start:c.end]
		if !utf8.Valid(text) {
			continue
		}
		out = append(out, Snippet{Path: c.file.path, Rel: c.file.rel, Line: c.line + 1, Text: string(text), Score: scores[id]})
	}
	return out
}
