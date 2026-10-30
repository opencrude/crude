package cmake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

func (l Limits) validated() (Limits, error) {
	d := DefaultLimits()
	pairs := [][2]*int{
		{&l.MaxTotalBytes, &d.MaxTotalBytes}, {&l.MaxArtifactBytes, &d.MaxArtifactBytes},
		{&l.MaxArtifacts, &d.MaxArtifacts}, {&l.MaxValues, &d.MaxValues},
		{&l.MaxDepth, &d.MaxDepth}, {&l.MaxItems, &d.MaxItems}, {&l.MaxStringBytes, &d.MaxStringBytes},
	}
	for _, p := range pairs {
		if *p[0] < 0 || *p[0] > *p[1] {
			return Limits{}, ErrInvalid
		}
		if *p[0] == 0 {
			*p[0] = *p[1]
		}
	}
	return l, nil
}

type budget struct {
	ctx    context.Context
	limits Limits
	values int
}

func (b *budget) work(n int) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if n > b.limits.MaxValues-b.values {
		return ErrLimit
	}
	b.values += n
	return nil
}

// Arbitrary blocking Reader.Read cannot be interrupted; check before and after
// bounded 4 KiB calls, without goroutines. Reader errors after valid JSON still
// abort. One extra byte distinguishes exact-limit EOF from oversize input.
func read(ctx context.Context, r io.Reader, max int) ([]byte, error) {
	var out bytes.Buffer
	var buf [4096]byte
	empty := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(buf[:min(len(buf), max-out.Len()+1)])
		if n < 0 || n > min(len(buf), max-out.Len()+1) {
			return nil, ErrInvalid
		}
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if out.Len()+n > max {
			return nil, ErrLimit
		}
		if _, e := out.Write(buf[:n]); e != nil {
			return nil, e
		}
		if err == io.EOF {
			return out.Bytes(), nil
		}
		if err != nil {
			return nil, fmt.Errorf("read CMake artifact: %w", err)
		}
		if n == 0 {
			empty++
			if empty == 100 {
				return nil, io.ErrNoProgress
			}
		} else {
			empty = 0
		}
	}
}

// Reject replacement-decoding before encoding/json sees any string. Also bound
// decoded strings before Decoder.Token allocates them. Escaped surrogate pairs
// must be adjacent; literal UTF-8 is independently validated over the whole input.
func stringsValid(b *budget, raw []byte) error {
	if !utf8.Valid(raw) {
		return ErrInvalid
	}
	for i := 0; i < len(raw); i++ {
		if i%4096 == 0 {
			if err := b.ctx.Err(); err != nil {
				return err
			}
		}
		if raw[i] != '"' {
			continue
		}
		size := 0
		closed := false
		for i++; i < len(raw); i++ {
			if i%4096 == 0 {
				if err := b.ctx.Err(); err != nil {
					return err
				}
			}
			c := raw[i]
			if c == '"' {
				closed = true
				break
			}
			if c < 0x20 {
				return ErrInvalid
			}
			size++
			if c == '\\' {
				i++
				if i >= len(raw) {
					return ErrInvalid
				}
				switch raw[i] {
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				case 'u':
					v, ok := hex4(raw, i+1)
					if !ok {
						return ErrInvalid
					}
					i += 4
					switch {
					case v >= 0xd800 && v <= 0xdbff:
						if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
							return ErrInvalid
						}
						w, ok := hex4(raw, i+3)
						if !ok || w < 0xdc00 || w > 0xdfff {
							return ErrInvalid
						}
						i += 6
						size += 3
					case v >= 0xdc00 && v <= 0xdfff:
						return ErrInvalid
					case v >= 0x800:
						size += 2
					case v >= 0x80:
						size++
					}
				default:
					return ErrInvalid
				}
			}
			if size > b.limits.MaxStringBytes {
				return ErrLimit
			}
		}
		if !closed {
			return ErrInvalid
		}
	}
	return b.ctx.Err()
}

func hex4(raw []byte, start int) (int, bool) {
	if start+4 > len(raw) {
		return 0, false
	}
	v := 0
	for _, c := range raw[start : start+4] {
		v *= 16
		switch {
		case c >= '0' && c <= '9':
			v += int(c - '0')
		case c >= 'a' && c <= 'f':
			v += int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v += int(c-'A') + 10
		default:
			return 0, false
		}
	}
	return v, true
}

func parse(b *budget, raw []byte) (map[string]any, error) {
	if err := stringsValid(b, raw); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := value(b, d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrInvalid
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, ErrInvalid
	}
	return m, b.ctx.Err()
}

func value(b *budget, d *json.Decoder, depth int) (any, error) {
	if err := b.work(1); err != nil {
		return nil, err
	}
	t, err := d.Token()
	if err != nil {
		return nil, ErrInvalid
	}
	delim, container := t.(json.Delim)
	if !container {
		return t, nil
	}
	if depth >= b.limits.MaxDepth {
		return nil, ErrLimit
	}
	switch delim {
	case '{':
		m := make(map[string]any)
		for d.More() {
			if len(m) >= b.limits.MaxItems {
				return nil, ErrLimit
			}
			if err := b.work(1); err != nil {
				return nil, err
			}
			k, err := d.Token()
			if err != nil {
				return nil, ErrInvalid
			}
			key, ok := k.(string)
			if !ok {
				return nil, ErrInvalid
			}
			if _, exists := m[key]; exists {
				return nil, ErrInvalid
			}
			v, err := value(b, d, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = v
		}
		if t, err := d.Token(); err != nil || t != json.Delim('}') {
			return nil, ErrInvalid
		}
		return m, nil
	case '[':
		var a []any
		for d.More() {
			if len(a) >= b.limits.MaxItems {
				return nil, ErrLimit
			}
			v, err := value(b, d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		if t, err := d.Token(); err != nil || t != json.Delim(']') {
			return nil, ErrInvalid
		}
		return a, nil
	default:
		return nil, ErrInvalid
	}
}
