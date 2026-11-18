package lspinlay

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

func validateJSON(raw []byte, depth int) error {
	if len(raw) > MaxRawBytes || !utf8.Valid(raw) {
		return invalid("raw byte bound exceeded or invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := jsonValue(decoder, depth); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalid("trailing JSON input")
	}
	return validateSurrogates(raw)
}

func jsonValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if value, ok := token.(string); ok && len(value) > MaxMetadataStringBytes {
		return invalid("metadata string byte bound exceeded")
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	if depth >= MaxDepth {
		return invalid("JSON depth bound exceeded")
	}
	count := 0
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return invalid("duplicate or invalid member")
			}
			if len(name) > MaxMetadataStringBytes {
				return invalid("member name byte bound exceeded")
			}
			seen[name] = true
			count++
			if count > MaxMetadataItems {
				return invalid("object member count bound exceeded")
			}
			if err := jsonValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			count++
			if count > MaxMetadataItems {
				return invalid("array item count bound exceeded")
			}
			if err := jsonValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return invalid("unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

// encoding/json replaces unpaired surrogate escapes. Reject them, including in
// opaque values and keys. JSON syntax has already established safe escape offsets.
func validateSurrogates(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		n, _ := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		i += 4
		if n < 0xd800 || n > 0xdfff {
			continue
		}
		if n > 0xdbff || i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return invalid("unpaired JSON surrogate")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return invalid("unpaired JSON surrogate")
		}
		i += 6
	}
	return nil
}
