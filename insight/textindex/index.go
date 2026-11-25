// Package textindex keeps an in-memory text index of one workspace. The core
// builds it in the background, the file watch keeps it current one file at a
// time, and Errand suggestions use it to retrieve related code (Related ranks
// 40-line chunks by BM25 over identifiers). Files lists the indexed files in
// the order of the Find in Files walk; Find in Files itself still reads the
// disk until the index's coverage is accepted for it.
//
// It is language-neutral: tokens are identifiers, never language keywords.
// Memory is bounded over text, chunks and postings together; a file that does
// not fit keeps only its path, and the index reports itself incomplete.
//
// Coverage rules: a build swaps in a complete corpus at once, so readers never
// see half of one. A change to an ignore file, a full update queue or a lost
// file watch schedules a new build; until it lands, the index is incomplete
// and Related returns nothing rather than results from stale rules.
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

	"crude/third_party/fastwalk"
	"crude/workspace/fileio"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

const (
	// DefaultMemoryLimit bounds the memory that the index keeps.
	DefaultMemoryLimit = 64 << 20
	// MaxIndexedFileBytes is the largest file whose text is kept and ranked.
	MaxIndexedFileBytes = 1 << 20
	chunkLines          = 40
	minTokenLength      = 3
	maxQueryTokens      = 24
	bm25K1              = 1.2
	bm25B               = 0.75

	// Memory estimates for the structures beside the text, in bytes. They
	// are deliberately generous: Go map buckets, string headers and slice
	// headers, rounded up.
	fileOverhead    = 160 // file struct, map entry, order slot
	chunkOverhead   = 96  // chunk struct and map entry
	termRefOverhead = 16  // string header in chunk.terms
	postingOverhead = 24  // one chunk entry in a term's postings
	termOverhead    = 112 // a new term: string, outer map entry, inner map
)

// liveMemory is the estimated memory of every open index in this process.
var liveMemory atomic.Int64

// TotalMemory returns the estimated memory of every open index, for the
// performance dashboard.
func TotalMemory() int64 { return liveMemory.Load() }

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
	Ready       bool
	Files       int
	TextBytes   int64
	MemoryBytes int64
	Chunks      int
	Tokens      int
	Incomplete  bool
	Builds      int
	BuildTime   time.Duration
}

type file struct {
	path, rel  string
	text       []byte
	large      bool
	binary     bool
	unreadable bool
	ignored    bool // in an ignore file: listed, never ranked
	chunks     []uint32
	cost       int64 // memory of this file's chunks, terms and postings
}

type chunk struct {
	file   *file
	line   int // 0-based first line
	start  int // byte offsets into file.text
	end    int
	length int // token count
	terms  []string
}

// corpus is one complete generation of indexed content. Builds fill a new
// corpus off the lock and swap it in; file changes edit the live one.
type corpus struct {
	files     map[string]*file // by absolute path
	order     []*file          // walk order, rebuilt lazily
	dirty     bool
	chunks    map[uint32]*chunk
	postings  map[string]map[uint32]uint16
	totalLen  int64
	nextChunk uint32
	textBytes int64
	memory    int64
	// overLimit: a file did not fit and keeps only its path.
	overLimit bool
	ignore    gitignore.Matcher
}

func newCorpus() *corpus {
	return &corpus{files: map[string]*file{}, chunks: map[uint32]*chunk{}, postings: map[string]map[uint32]uint16{}}
}

// Index is safe for concurrent use. Readers never wait for a build.
type Index struct {
	demand  chan struct{}
	request sync.Once
	root    string
	limit   int64

	mu     sync.RWMutex
	live   *corpus
	stale  bool   // a rebuild is due: ignore rules changed or changes were lost
	asked  uint64 // counts Rescan requests; a build clears only those before it
	builds int
	took   time.Duration
	ready  atomic.Bool
	closed atomic.Bool

	updates chan string
	rescan  chan struct{}

	life   sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// New returns an empty index for root. Start builds it.
func New(root string, memoryLimit int64) *Index {
	if memoryLimit <= 0 {
		memoryLimit = DefaultMemoryLimit
	}
	return &Index{demand: make(chan struct{}), root: filepath.Clean(root), limit: memoryLimit, live: newCorpus(),
		updates: make(chan string, 1024), rescan: make(chan struct{}, 1)}
}

// Start builds the index on its own worker and then applies changes in
// order. It runs once; later calls and calls after Close do nothing.
func (x *Index) Start() {
	x.life.Lock()
	defer x.life.Unlock()
	if x.cancel != nil || x.closed.Load() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	x.cancel, x.done = cancel, make(chan struct{})
	go x.run(ctx)
}

func (x *Index) run(ctx context.Context) {
	defer close(x.done)
	if !x.rebuild(ctx) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-x.rescan:
			if !x.rebuild(ctx) {
				return
			}
		case path := <-x.updates:
			x.refresh(ctx, path)
		}
	}
}

// Close stops the worker and joins it. The index stays readable but no
// longer changes. Close may be called more than once.
func (x *Index) Close() {
	x.life.Lock()
	defer x.life.Unlock()
	if x.closed.Swap(true) {
		return
	}
	if x.cancel != nil {
		x.cancel()
		<-x.done
	}
	x.ready.Store(false)
	x.mu.Lock()
	liveMemory.Add(-x.live.memory)
	x.live = newCorpus()
	x.mu.Unlock()
}

// Demand closes on the first content query. A broad workspace uses it to
// establish complete file observation before starting the index worker.
func (x *Index) Demand() <-chan struct{} {
	if x == nil {
		return nil
	}
	return x.demand
}

// Ready reports a finished first build. Before it, readers use the disk.
func (x *Index) Ready() bool { return x != nil && x.ready.Load() }

// Changed queues one changed, created or removed path. It never blocks; a
// full queue schedules a new build so no change is lost.
func (x *Index) Changed(path string) {
	if x == nil || x.closed.Load() {
		return
	}
	select {
	case x.updates <- filepath.Clean(path):
	default:
		x.Rescan()
	}
}

// Rescan schedules a new build, for example after the file watch lost
// events. Until it lands the index is incomplete. Requests coalesce.
func (x *Index) Rescan() {
	if x == nil || x.closed.Load() {
		return
	}
	x.mu.Lock()
	x.stale = true
	x.asked++
	x.mu.Unlock()
	select {
	case x.rescan <- struct{}{}:
	default:
	}
}

// Complete reports whether every searchable file's text is in memory with
// current ignore rules, so readers can trust the index.
func (x *Index) Complete() bool {
	if !x.Ready() {
		return false
	}
	x.mu.RLock()
	defer x.mu.RUnlock()
	return !x.stale && !x.live.overLimit
}

// Stats returns a snapshot for diagnostics.
func (x *Index) Stats() Stats {
	x.mu.RLock()
	defer x.mu.RUnlock()
	c := x.live
	return Stats{Ready: x.ready.Load(), Files: len(c.files), TextBytes: c.textBytes, MemoryBytes: c.memory,
		Chunks: len(c.chunks), Tokens: len(c.postings), Incomplete: x.stale || c.overLimit,
		Builds: x.builds, BuildTime: x.took}
}

// rebuild walks the tree into a new corpus and swaps it in. It returns false
// when the context ended.
func (x *Index) rebuild(ctx context.Context) bool {
	began := time.Now()
	// The old corpus stays served (and stale) during the walk. A request
	// that arrives during the walk keeps the result stale and asks again.
	// Drain the wake first, then read the count: a request after the drain
	// leaves a new wake, so a stale result always has a build queued.
	select {
	case <-x.rescan:
	default:
	}
	x.mu.Lock()
	asked := x.asked
	x.mu.Unlock()
	next, err := x.build(ctx)
	if err != nil {
		return false
	}
	x.mu.Lock()
	x.stale = x.asked != asked
	liveMemory.Add(next.memory - x.live.memory)
	x.live = next
	x.builds++
	x.took = time.Since(began)
	x.mu.Unlock()
	x.ready.Store(true)
	return true
}

type ignoreFile struct {
	depth    int
	dir      string
	patterns []gitignore.Pattern
}

func (x *Index) build(ctx context.Context) (*corpus, error) {
	type found struct {
		path string
		size int64
	}
	var mu sync.Mutex
	var paths []found
	var ignores []ignoreFile
	conf := &fastwalk.Config{NumWorkers: min(8, fastwalk.DefaultNumWorkers())}
	err := fastwalk.WalkContext(ctx, conf, x.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != x.root && fileio.ShouldSkipProjectDir(entry.Name(), false) {
				return fs.SkipDir
			}
			if patterns := readIgnore(x.root, path); len(patterns) > 0 {
				mu.Lock()
				ignores = append(ignores, ignoreFile{depth: depthOf(x.root, path), dir: path, patterns: patterns})
				mu.Unlock()
			}
			return nil
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		size := int64(fileio.MaxProjectSearchFileSize + 1)
		if info, err := entry.Info(); err == nil {
			size = info.Size()
		}
		mu.Lock()
		paths = append(paths, found{path, size})
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	c := newCorpus()
	c.ignore = matcherOf(ignores)
	for _, f := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		x.load(c, f.path, f.size, false)
	}
	return c, nil
}

// matcherOf orders ignore files from the root down, so a deeper file's rules
// come later and win, as in Git. The parallel walk finds them in any order.
func matcherOf(files []ignoreFile) gitignore.Matcher {
	slices.SortFunc(files, func(a, b ignoreFile) int {
		if a.depth != b.depth {
			return a.depth - b.depth
		}
		return strings.Compare(a.dir, b.dir)
	})
	var patterns []gitignore.Pattern
	for _, f := range files {
		patterns = append(patterns, f.patterns...)
	}
	return gitignore.NewMatcher(patterns)
}

func depthOf(root, dir string) int {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(filepath.ToSlash(rel), "/") + 1
}

func readIgnore(root, dir string) []gitignore.Pattern {
	data, err := fileio.ReadFileSharedDelete(filepath.Join(dir, ".gitignore"))
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

// refresh applies one change to the live corpus. A changed ignore file
// schedules a build, because it changes which files rank.
func (x *Index) refresh(ctx context.Context, path string) {
	if filepath.Base(path) == ".gitignore" {
		x.Rescan()
		return
	}
	info, err := os.Lstat(path)
	switch {
	case err == nil && info.IsDir():
		x.refreshDir(ctx, path)
	case err == nil && info.Mode().IsRegular():
		x.mu.Lock()
		c := x.live
		x.mu.Unlock()
		x.load(c, path, info.Size(), true)
	default:
		// Removed (or no longer a regular file): a removed directory takes
		// its files with it.
		x.mu.Lock()
		before := x.live.memory
		x.live.remove(path)
		prefix := path + string(filepath.Separator)
		for p := range x.live.files {
			if strings.HasPrefix(p, prefix) {
				x.live.remove(p)
			}
		}
		liveMemory.Add(x.live.memory - before)
		x.mu.Unlock()
	}
}

// refreshDir indexes a created or renamed directory with the same rules as a
// build. A directory that brings its own ignore file schedules a build.
func (x *Index) refreshDir(ctx context.Context, dir string) {
	if _, ok := x.searchable(filepath.Join(dir, "x")); !ok {
		return
	}
	x.mu.Lock()
	c := x.live
	x.mu.Unlock()
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if path != dir && fileio.ShouldSkipProjectDir(entry.Name(), false) {
				return fs.SkipDir
			}
			if _, err := os.Lstat(filepath.Join(path, ".gitignore")); err == nil {
				x.Rescan()
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") || !entry.Type().IsRegular() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			x.load(c, path, info.Size(), true)
		}
		return nil
	})
}

// load reads one file and puts it in c. When c is the live corpus (live),
// the edit happens under the lock; a corpus being built is private.
func (x *Index) load(c *corpus, path string, size int64, live bool) {
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
	if live {
		x.mu.Lock()
		defer x.mu.Unlock()
		if c != x.live {
			return // a build replaced the corpus; it read the file itself
		}
		before := c.memory
		defer func() { liveMemory.Add(c.memory - before) }()
	}
	c.remove(path)
	if c.ignore != nil {
		f.ignored = c.ignore.Match(strings.Split(rel, "/"), false)
	}
	c.add(f, x.limit)
}

// add inserts f and its chunks, keeping memory within limit. A file that
// does not fit keeps only its path.
func (c *corpus) add(f *file, limit int64) {
	base := int64(fileOverhead + len(f.path) + len(f.rel))
	if f.text != nil && c.memory+base+int64(len(f.text)) > limit {
		f.text, c.overLimit = nil, true
	}
	c.files[f.path] = f
	c.dirty = true
	c.textBytes += int64(len(f.text))
	c.memory += base + int64(len(f.text))
	if f.text == nil || f.ignored || len(f.text) > MaxIndexedFileBytes {
		return
	}
	c.chunk(f)
	if c.memory > limit {
		// Its chunks did not fit: drop them and the text.
		c.unchunk(f)
		c.textBytes -= int64(len(f.text))
		c.memory -= int64(len(f.text))
		f.text, c.overLimit = nil, true
	}
}

func (c *corpus) remove(path string) {
	f := c.files[path]
	if f == nil {
		return
	}
	c.unchunk(f)
	c.textBytes -= int64(len(f.text))
	c.memory -= int64(fileOverhead+len(f.path)+len(f.rel)) + int64(len(f.text))
	delete(c.files, path)
	c.dirty = true
}

func (c *corpus) unchunk(f *file) {
	for _, id := range f.chunks {
		ch := c.chunks[id]
		for _, term := range ch.terms {
			if p := c.postings[term]; p != nil {
				delete(p, id)
				if len(p) == 0 {
					delete(c.postings, term)
				}
			}
		}
		c.totalLen -= int64(ch.length)
		delete(c.chunks, id)
	}
	f.chunks = nil
	c.memory -= f.cost
	f.cost = 0
}

// chunk splits f into chunks of chunkLines lines and posts their terms.
// Term memory is charged to the file that first adds the term and stays
// charged until that file goes; this over-counts shared terms slightly,
// which keeps the limit conservative.
func (c *corpus) chunk(f *file) {
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
			id := c.nextChunk
			c.nextChunk++
			ch := &chunk{file: f, line: line, start: start, end: end, length: length, terms: make([]string, 0, len(counts))}
			cost := int64(chunkOverhead)
			for term, n := range counts {
				p := c.postings[term]
				if p == nil {
					p = map[uint32]uint16{}
					c.postings[term] = p
					cost += int64(termOverhead + len(term))
				}
				p[id] = n
				ch.terms = append(ch.terms, term)
				cost += termRefOverhead + postingOverhead
			}
			c.chunks[id] = ch
			f.chunks = append(f.chunks, id)
			c.totalLen += int64(length)
			f.cost += cost
			c.memory += cost
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
	defer x.mu.Unlock()
	c := x.live
	if c.dirty || c.order == nil {
		c.order = c.order[:0]
		for _, f := range c.files {
			c.order = append(c.order, f)
		}
		slices.SortFunc(c.order, func(a, b *file) int { return compareWalkOrder(a.rel, b.rel) })
		c.dirty = false
	}
	out := make([]fileio.ProjectFile, len(c.order))
	for i, f := range c.order {
		out[i] = fileio.ProjectFile{Path: f.path, Rel: f.rel, Text: f.text, Large: f.large, Binary: f.binary, Unreadable: f.unreadable}
	}
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
// for a build and returns nothing while coverage is incomplete.
func (x *Index) Related(query string, limit int, skip func(path string, line int) bool) []Snippet {
	if x == nil || limit <= 0 {
		return nil
	}
	x.request.Do(func() { close(x.demand) })
	if !x.Ready() {
		return nil
	}
	tokens := Tokens(query)
	x.mu.RLock()
	defer x.mu.RUnlock()
	c := x.live
	if x.stale || c.overLimit {
		return nil
	}
	n := float64(len(c.chunks))
	if n == 0 {
		return nil
	}
	avg := float64(c.totalLen) / n
	type weighted struct {
		term string
		idf  float64
	}
	terms := make([]weighted, 0, len(tokens))
	for _, t := range tokens {
		df := float64(len(c.postings[t]))
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
		for id, tf := range c.postings[t.term] {
			ch := c.chunks[id]
			f := float64(tf)
			scores[id] += t.idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(ch.length)/avg))
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
		ch := c.chunks[id]
		if skip != nil && skip(ch.file.path, ch.line+1) {
			continue
		}
		text := ch.file.text[ch.start:ch.end]
		if !utf8.Valid(text) {
			continue
		}
		out = append(out, Snippet{Path: ch.file.path, Rel: ch.file.rel, Line: ch.line + 1, Text: string(text), Score: scores[id]})
	}
	return out
}
