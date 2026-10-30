package project

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"unicode/utf16"
	"unicode/utf8"
)

// ParseCompilationDatabase parses one JSON array entry by entry, atomically.
// It returns a zero database on every error, including reader errors after the
// closing array. Unknown extension values are validated and discarded within
// the same budgets; they never add observations or coverage.
//
// The scanner checks bounds before growing token/collection storage; it never
// uses encoding/json's potentially oversized token allocations or retains an
// entire raw mirror. Duplicate keys (including equal/escaped duplicates) in ANY
// object, invalid UTF-8, unpaired surrogate escapes and unsafe known-field shapes
// fail closed. Required directory/file and the executable argv[0] or command must
// be nonempty. No path, shell dialect or build semantics are inferred.
//
// Cancellation is checked during scanning and before/after each bounded read
// and hash. No goroutines are started: an arbitrary Reader.Read cannot be
// interrupted while it blocks. Reader errors remain visible via errors.Is.
func ParseCompilationDatabase(ctx context.Context, r io.Reader, limits Limits) (CompilationDatabase, error) {
	if ctx == nil || r == nil {
		return CompilationDatabase{}, &Error{Kind: ErrInvalid, Reason: "nil context or reader"}
	}
	l, err := limits.validated()
	if err != nil {
		return CompilationDatabase{}, err
	}
	if err := ctx.Err(); err != nil {
		return CompilationDatabase{}, err
	}
	source := &observedReader{ctx: ctx, r: r, max: l.MaxInputBytes, digest: sha256.New()}
	p := parser{ctx: ctx, r: bufio.NewReaderSize(source, 4096), limits: l}
	if err := p.charge(256); err != nil {
		return CompilationDatabase{}, err
	}
	rows, err := p.database()
	if err == nil {
		_, err = p.nonspace()
		if errors.Is(err, io.EOF) {
			err = nil
		} else if err == nil {
			err = p.invalid("trailing JSON data")
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return CompilationDatabase{}, err
	}
	var digest [32]byte
	copy(digest[:], source.digest.Sum(nil))
	return CompilationDatabase{
		artifact: ArtifactIdentity{
			SHA256: digest, ObservedBytes: source.count,
			Format: CompilationDatabaseFormat, ImporterVersion: ImporterVersion,
		},
		entries: rows, accountedBytes: p.accounted,
	}, nil
}

// Read at most the remaining budget plus one probe byte. The probe distinguishes
// exact-limit EOF from oversized input, including whitespace after the JSON.
// Hash work is bounded to at most 4 KiB/read by the private bufio reader.
type observedReader struct {
	ctx        context.Context
	r          io.Reader
	max, count int64
	digest     hash.Hash
}

func (r *observedReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	remaining := r.max - r.count
	if int64(len(b)) > remaining+1 {
		b = b[:remaining+1]
	}
	n, err := r.r.Read(b)
	if n < 0 || n > len(b) {
		return 0, &Error{Kind: ErrInvalid, Offset: r.count, Reason: "invalid reader byte count"}
	}
	r.count += int64(n)
	if r.count > r.max {
		return 0, &Error{Kind: ErrLimit, Offset: r.count, Reason: "input bytes"}
	}
	if cancelErr := r.ctx.Err(); cancelErr != nil {
		return 0, cancelErr
	}
	// hash.Hash.Write is specified never to return an error.
	_, _ = r.digest.Write(b[:n])
	if cancelErr := r.ctx.Err(); cancelErr != nil {
		return 0, cancelErr
	}
	if err != nil && !errors.Is(err, io.EOF) {
		err = fmt.Errorf("read compilation database: %w", err)
	}
	return n, err
}

type parser struct {
	ctx               context.Context
	r                 *bufio.Reader
	limits            Limits
	offset, accounted int64
	values            int
}

func (p *parser) invalid(reason string) error {
	return &Error{Kind: ErrInvalid, Offset: p.offset, Reason: reason}
}

func (p *parser) limit(reason string) error {
	return &Error{Kind: ErrLimit, Offset: p.offset, Reason: reason}
}

func (p *parser) charge(n int64) error {
	if n > p.limits.MaxAccountedBytes-p.accounted {
		return p.limit("accounted bytes")
	}
	p.accounted += n
	return nil
}

func (p *parser) byte() (byte, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	b, err := p.r.ReadByte()
	if err == nil {
		p.offset++
	}
	return b, err
}

func (p *parser) required() (byte, error) {
	b, err := p.byte()
	if errors.Is(err, io.EOF) {
		err = p.invalid("unexpected end of JSON")
	}
	return b, err
}

func (p *parser) nonspace() (byte, error) {
	for {
		b, err := p.byte()
		if err != nil || !space(b) {
			return b, err
		}
	}
}

func space(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

func (p *parser) next() (byte, error) {
	b, err := p.nonspace()
	if errors.Is(err, io.EOF) {
		err = p.invalid("unexpected end of JSON")
	}
	return b, err
}

func (p *parser) expect(want byte) error {
	b, err := p.next()
	if err != nil {
		return err
	}
	if b != want {
		return p.invalid("unexpected JSON delimiter")
	}
	return nil
}

func (p *parser) value() error {
	if p.values >= p.limits.MaxValues {
		return p.limit("JSON values")
	}
	p.values++
	return nil
}

func (p *parser) depth(depth int) error {
	if depth > p.limits.MaxDepth {
		return p.limit("JSON depth")
	}
	return nil
}

// Collection growth is explicit, with the whole new allocation charged BEFORE
// make. Superseded capacities remain charged; no hidden append growth is used.
func grow[T any](p *parser, s []T, max int, slotBytes int64) ([]T, error) {
	if len(s) == max {
		return nil, p.limit("collection count")
	}
	if len(s) < cap(s) {
		return s, nil
	}
	c := maxCapacity(cap(s), max)
	if err := p.charge(int64(c) * slotBytes); err != nil {
		return nil, err
	}
	out := make([]T, len(s), c)
	copy(out, s)
	return out, nil
}

func maxCapacity(old, max int) int {
	c := old * 2
	if c < 8 {
		c = 8
	}
	if c > max {
		c = max
	}
	return c
}

func (p *parser) database() ([]Entry, error) {
	if err := p.value(); err != nil {
		return nil, err
	}
	if err := p.depth(1); err != nil {
		return nil, err
	}
	if err := p.expect('['); err != nil {
		return nil, err
	}
	var entries []Entry
	b, err := p.next()
	if err != nil || b == ']' {
		return entries, err
	}
	for {
		if len(entries) >= p.limits.MaxEntries {
			return nil, p.limit("entry count")
		}
		if b != '{' {
			return nil, p.invalid("entry must be an object")
		}
		// Includes the staged entry before it enters the ordered slice.
		if err := p.charge(128); err != nil {
			return nil, err
		}
		var e Entry
		err = p.object(2, func(key string, first byte) error {
			switch key {
			case "directory":
				e.Directory, err = p.stringValue(first)
			case "file":
				e.File, err = p.stringValue(first)
			case "arguments":
				e.HasArguments = true
				e.Arguments, err = p.arguments(first, 3)
			case "command":
				e.HasCommand = true
				e.Command, err = p.stringValue(first)
			case "output":
				e.HasOutput = true
				e.Output, err = p.stringValue(first)
			default:
				err = p.skip(first, 3)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
		if e.Directory == "" || e.File == "" || (!e.HasArguments && !e.HasCommand) ||
			(e.HasCommand && e.Command == "") {
			return nil, p.invalid("missing or empty required entry fields")
		}
		entries, err = grow(p, entries, p.limits.MaxEntries, 128)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
		b, err = p.next()
		if err != nil || b == ']' {
			return entries, err
		}
		if b != ',' {
			return nil, p.invalid("expected entry separator")
		}
		b, err = p.next()
		if err != nil {
			return nil, err
		}
	}
}

// object consumes an object whose opening brace was consumed. Each key is
// decoded before duplicate detection, including keys in skipped extensions.
// The conservative map allowance is 128 base + 256/member, plus key storage.
func (p *parser) object(depth int, member func(string, byte) error) error {
	if err := p.value(); err != nil {
		return err
	}
	if err := p.depth(depth); err != nil {
		return err
	}
	if err := p.charge(128); err != nil {
		return err
	}
	seen := make(map[string]struct{})
	b, err := p.next()
	if err != nil || b == '}' {
		return err
	}
	for {
		if len(seen) >= p.limits.MaxObjectMembers {
			return p.limit("object member count")
		}
		if b != '"' {
			return p.invalid("object key must be a string")
		}
		key, err := p.string()
		if err != nil {
			return err
		}
		if _, exists := seen[key]; exists {
			return p.invalid("duplicate object key")
		}
		if err := p.charge(256); err != nil {
			return err
		}
		seen[key] = struct{}{}
		if err := p.expect(':'); err != nil {
			return err
		}
		first, err := p.next()
		if err != nil {
			return err
		}
		if err := member(key, first); err != nil {
			return err
		}
		b, err = p.next()
		if err != nil || b == '}' {
			return err
		}
		if b != ',' {
			return p.invalid("expected object member separator")
		}
		b, err = p.next()
		if err != nil {
			return err
		}
	}
}

func (p *parser) stringValue(first byte) (string, error) {
	if err := p.value(); err != nil {
		return "", err
	}
	if first != '"' {
		return "", p.invalid("expected string")
	}
	return p.string()
}

func (p *parser) arguments(first byte, depth int) ([]string, error) {
	if err := p.value(); err != nil {
		return nil, err
	}
	if err := p.depth(depth); err != nil {
		return nil, err
	}
	if first != '[' {
		return nil, p.invalid("arguments must be an array")
	}
	var args []string
	b, err := p.next()
	if err != nil {
		return nil, err
	}
	if b == ']' {
		return nil, p.invalid("arguments must contain an executable")
	}
	for {
		if len(args) >= p.limits.MaxArguments {
			return nil, p.limit("argument count")
		}
		s, err := p.stringValue(b)
		if err != nil {
			return nil, err
		}
		if len(args) == 0 && s == "" {
			return nil, p.invalid("empty executable argument")
		}
		args, err = grow(p, args, p.limits.MaxArguments, 16)
		if err != nil {
			return nil, err
		}
		args = append(args, s)
		b, err = p.next()
		if err != nil || b == ']' {
			return args, err
		}
		if b != ',' {
			return nil, p.invalid("expected argument separator")
		}
		b, err = p.next()
		if err != nil {
			return nil, err
		}
	}
}

func (p *parser) skip(first byte, depth int) error {
	switch first {
	case '{':
		return p.object(depth, func(_ string, b byte) error { return p.skip(b, depth+1) })
	case '[':
		if err := p.value(); err != nil {
			return err
		}
		if err := p.depth(depth); err != nil {
			return err
		}
		b, err := p.next()
		if err != nil || b == ']' {
			return err
		}
		for {
			if err := p.skip(b, depth+1); err != nil {
				return err
			}
			b, err = p.next()
			if err != nil || b == ']' {
				return err
			}
			if b != ',' {
				return p.invalid("expected array separator")
			}
			b, err = p.next()
			if err != nil {
				return err
			}
		}
	case '"':
		_, err := p.stringValue(first)
		return err
	default:
		if err := p.value(); err != nil {
			return err
		}
		switch first {
		case 't':
			return p.literal("true")
		case 'f':
			return p.literal("false")
		case 'n':
			return p.literal("null")
		default:
			return p.number(first)
		}
	}
}

func (p *parser) literal(full string) error {
	if len(full) > p.limits.MaxTokenBytes {
		return p.limit("token bytes")
	}
	// The dispatch already consumed the first byte.
	for i := 1; i < len(full); i++ {
		b, err := p.required()
		if err != nil {
			return err
		}
		if b != full[i] {
			return p.invalid("invalid JSON literal")
		}
	}
	return nil
}

// A small grammar state machine validates skipped numbers without collecting
// number bytes or converting them to a host-dependent numeric representation.
func (p *parser) number(first byte) error {
	const (
		start = iota
		minus
		zero
		integer
		dot
		fraction
		exponent
		exponentSign
		exponentDigits
	)
	state, count, b := start, 0, first
	for {
		count++
		if count > p.limits.MaxTokenBytes {
			return p.limit("token bytes")
		}
		switch {
		case (state == start && b == '-'):
			state = minus
		case (state == start || state == minus) && b == '0':
			state = zero
		case (state == start || state == minus) && b >= '1' && b <= '9':
			state = integer
		case (state == integer || state == fraction || state == exponentDigits) && b >= '0' && b <= '9':
		case (state == zero || state == integer) && b == '.':
			state = dot
		case state == dot && b >= '0' && b <= '9':
			state = fraction
		case (state == zero || state == integer || state == fraction) && (b == 'e' || b == 'E'):
			state = exponent
		case state == exponent && (b == '+' || b == '-'):
			state = exponentSign
		case (state == exponent || state == exponentSign) && b >= '0' && b <= '9':
			state = exponentDigits
		default:
			return p.invalid("invalid JSON number")
		}
		var err error
		b, err = p.byte()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		end := err != nil || space(b) || b == ',' || b == ']' || b == '}'
		if end {
			if err == nil {
				if err := p.r.UnreadByte(); err != nil {
					return err
				}
				p.offset--
			}
			if state == zero || state == integer || state == fraction || state == exponentDigits {
				return nil
			}
			return p.invalid("incomplete JSON number")
		}
	}
}

// string scans one bounded decoded UTF-8 value, with no raw JSON mirror. Both
// encoded token length and decoded length are checked before storage growth.
func (p *parser) string() (string, error) {
	start := p.offset - 1
	var out []byte
	for {
		b, err := p.stringByte(start)
		if err != nil {
			return "", err
		}
		if b == '"' {
			if !utf8.Valid(out) {
				return "", p.invalid("invalid UTF-8")
			}
			if err := p.charge(int64(len(out))); err != nil {
				return "", err
			}
			return string(out), nil
		}
		if b < 0x20 {
			return "", p.invalid("control byte in string")
		}
		if b == '\\' {
			b, err = p.stringByte(start)
			if err != nil {
				return "", err
			}
			switch b {
			case '"', '\\', '/':
			case 'b':
				b = '\b'
			case 'f':
				b = '\f'
			case 'n':
				b = '\n'
			case 'r':
				b = '\r'
			case 't':
				b = '\t'
			case 'u':
				r, err := p.unicodeEscape(start)
				if err != nil {
					return "", err
				}
				var encoded [utf8.UTFMax]byte
				n := utf8.EncodeRune(encoded[:], r)
				for _, c := range encoded[:n] {
					out, err = p.appendByte(out, c)
					if err != nil {
						return "", err
					}
				}
				continue
			default:
				return "", p.invalid("invalid string escape")
			}
		}
		out, err = p.appendByte(out, b)
		if err != nil {
			return "", err
		}
	}
}

func (p *parser) stringByte(start int64) (byte, error) {
	if p.offset-start >= int64(p.limits.MaxTokenBytes) {
		return 0, p.limit("token bytes")
	}
	return p.required()
}

func (p *parser) appendByte(out []byte, b byte) ([]byte, error) {
	if len(out) >= p.limits.MaxStringBytes {
		return nil, p.limit("string bytes")
	}
	grown, err := grow(p, out, p.limits.MaxStringBytes, 1)
	if err != nil {
		return nil, err
	}
	return append(grown, b), nil
}

func (p *parser) hex4(start int64) (rune, error) {
	var r rune
	for range 4 {
		b, err := p.stringByte(start)
		if err != nil {
			return 0, err
		}
		r <<= 4
		switch {
		case b >= '0' && b <= '9':
			r += rune(b - '0')
		case b >= 'a' && b <= 'f':
			r += rune(b-'a') + 10
		case b >= 'A' && b <= 'F':
			r += rune(b-'A') + 10
		default:
			return 0, p.invalid("invalid Unicode escape")
		}
	}
	return r, nil
}

func (p *parser) unicodeEscape(start int64) (rune, error) {
	r, err := p.hex4(start)
	if err != nil || !utf16.IsSurrogate(r) {
		return r, err
	}
	if r < 0xd800 || r > 0xdbff {
		return 0, p.invalid("unpaired Unicode surrogate")
	}
	for _, want := range []byte{'\\', 'u'} {
		b, err := p.stringByte(start)
		if err != nil {
			return 0, err
		}
		if b != want {
			return 0, p.invalid("unpaired Unicode surrogate")
		}
	}
	low, err := p.hex4(start)
	if err != nil {
		return 0, err
	}
	if low < 0xdc00 || low > 0xdfff {
		return 0, p.invalid("unpaired Unicode surrogate")
	}
	return utf16.DecodeRune(r, low), nil
}
