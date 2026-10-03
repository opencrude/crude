package skills

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
	"golang.org/x/text/unicode/norm"
)

var toolRequestPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*(\([A-Za-z0-9_./:*,+@=-]+\))?$`)

func readFrontmatter(ctx context.Context, root *os.Root) ([]byte, error) {
	file, before, _, err := openRegular(root, "SKILL.md", MaxDefinitionBytes)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(io.LimitReader(file, MaxFrontmatterBytes+16))
	first, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("%w: missing YAML frontmatter", ErrInvalid)
	}
	if strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(first, "\ufeff"), "\n"), "\r") != "---" {
		return nil, fmt.Errorf("%w: SKILL.md must start with YAML frontmatter", ErrInvalid)
	}
	var frontmatter bytes.Buffer
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, readErr := reader.ReadString('\n')
		if strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r") == "---" {
			after, err := file.Stat()
			if err != nil {
				return nil, err
			}
			if before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
				return nil, ErrChanged
			}
			return frontmatter.Bytes(), nil
		}
		frontmatter.WriteString(line)
		if frontmatter.Len() > MaxFrontmatterBytes {
			return nil, fmt.Errorf("%w: YAML frontmatter exceeds %d bytes", ErrLimit, MaxFrontmatterBytes)
		}
		if readErr != nil {
			return nil, fmt.Errorf("%w: missing closing YAML frontmatter delimiter", ErrInvalid)
		}
	}
}

func splitDefinition(definition []byte) ([]byte, []byte, error) {
	if !utf8.Valid(definition) || bytes.IndexByte(definition, 0) >= 0 {
		return nil, nil, fmt.Errorf("%w: SKILL.md must be UTF-8 text without NUL bytes", ErrInvalid)
	}
	definition = bytes.TrimPrefix(definition, []byte("\ufeff"))
	firstEnd := bytes.IndexByte(definition, '\n')
	if firstEnd < 0 || string(bytes.TrimSuffix(definition[:firstEnd], []byte("\r"))) != "---" {
		return nil, nil, fmt.Errorf("%w: SKILL.md must start with YAML frontmatter", ErrInvalid)
	}
	headerStart, offset := firstEnd+1, firstEnd+1
	for offset <= len(definition) {
		end := bytes.IndexByte(definition[offset:], '\n')
		if end < 0 {
			end = len(definition)
		} else {
			end += offset
		}
		if string(bytes.TrimSuffix(definition[offset:end], []byte("\r"))) == "---" {
			if offset-headerStart > MaxFrontmatterBytes {
				return nil, nil, fmt.Errorf("%w: YAML frontmatter exceeds %d bytes", ErrLimit, MaxFrontmatterBytes)
			}
			bodyStart := min(end+1, len(definition))
			if len(definition)-bodyStart > MaxInstructionBytes {
				return nil, nil, fmt.Errorf("%w: instructions exceed %d bytes", ErrLimit, MaxInstructionBytes)
			}
			return definition[headerStart:offset], definition[bodyStart:], nil
		}
		if end-headerStart > MaxFrontmatterBytes {
			return nil, nil, fmt.Errorf("%w: YAML frontmatter exceeds %d bytes", ErrLimit, MaxFrontmatterBytes)
		}
		if end == len(definition) {
			break
		}
		offset = end + 1
	}
	return nil, nil, fmt.Errorf("%w: missing closing YAML frontmatter delimiter", ErrInvalid)
}

func decodeFrontmatter(frontmatter []byte) (*yaml.Node, error) {
	if !utf8.Valid(frontmatter) || bytes.IndexByte(frontmatter, 0) >= 0 {
		return nil, fmt.Errorf("%w: frontmatter must be UTF-8 text", ErrInvalid)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(frontmatter))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: YAML frontmatter: %w", ErrInvalid, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: frontmatter must contain one YAML mapping", ErrInvalid)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: frontmatter must be a YAML mapping", ErrInvalid)
	}
	if err := validateYAMLNode(&document, 0); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

func validateYAMLNode(node *yaml.Node, depth int) error {
	if depth > 4 {
		return fmt.Errorf("%w: nested frontmatter structures are unsupported", ErrUnsupported)
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return fmt.Errorf("%w: YAML anchors and aliases are unsupported", ErrUnsupported)
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("%w: frontmatter keys must be strings", ErrInvalid)
			}
			if seen[key.Value] {
				return fmt.Errorf("%w: duplicate frontmatter key %q", ErrInvalid, key.Value)
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := validateYAMLNode(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func parseFrontmatter(frontmatter []byte, directoryName string) (Skill, error) {
	mapping, err := decodeFrontmatter(frontmatter)
	if err != nil {
		return Skill{}, err
	}
	skill := Skill{Metadata: make(map[string]string)}
	for index := 0; index < len(mapping.Content); index += 2 {
		key, value := mapping.Content[index].Value, mapping.Content[index+1]
		if key == "metadata" {
			if value.Kind != yaml.MappingNode || value.Tag != "!!map" {
				return Skill{}, fmt.Errorf("%w: metadata must map string keys to string values", ErrInvalid)
			}
			for entry := 0; entry < len(value.Content); entry += 2 {
				metadataKey, metadataValue := value.Content[entry], value.Content[entry+1]
				if metadataValue.Kind != yaml.ScalarNode || metadataValue.Tag != "!!str" {
					return Skill{}, fmt.Errorf("%w: metadata values must be strings", ErrInvalid)
				}
				skill.Metadata[metadataKey.Value] = metadataValue.Value
			}
			continue
		}
		if key != "name" && key != "description" && key != "license" && key != "compatibility" && key != "allowed-tools" {
			return Skill{}, fmt.Errorf("%w: unsupported frontmatter field %q; executable hooks and client-specific behavior are not supported", ErrUnsupported, key)
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return Skill{}, fmt.Errorf("%w: %s must be a string", ErrInvalid, key)
		}
		switch key {
		case "name":
			skill.Name = value.Value
		case "description":
			skill.Description = value.Value
		case "license":
			skill.License = value.Value
		case "compatibility":
			if strings.TrimSpace(value.Value) == "" || utf8.RuneCountInString(value.Value) > 500 {
				return Skill{}, fmt.Errorf("%w: compatibility must contain 1-500 characters", ErrInvalid)
			}
			skill.Compatibility = value.Value
		case "allowed-tools":
			seen := make(map[string]bool)
			for _, tool := range strings.Fields(value.Value) {
				if !toolRequestPattern.MatchString(tool) {
					return Skill{}, fmt.Errorf("%w: allowed-tools contains an unsupported executable expression", ErrUnsupported)
				}
				if !seen[tool] {
					skill.AllowedTools = append(skill.AllowedTools, tool)
					seen[tool] = true
				}
			}
		}
	}
	if err := validateName(skill.Name); err != nil {
		return Skill{}, err
	}
	if norm.NFKC.String(skill.Name) != norm.NFKC.String(directoryName) {
		return Skill{}, fmt.Errorf("%w: name must match the skill directory %q", ErrInvalid, directoryName)
	}
	if err := validateDescription(skill.Description); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func validateName(name string) error {
	normalized := norm.NFKC.String(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(normalized) < 1 || utf8.RuneCountInString(normalized) > 64 || strings.ToLower(normalized) != normalized || strings.HasPrefix(normalized, "-") || strings.HasSuffix(normalized, "-") || strings.Contains(normalized, "--") {
		return fmt.Errorf("%w: name must contain 1-64 lowercase letters or numbers with single separating hyphens", ErrInvalid)
	}
	for _, character := range normalized {
		if !unicode.IsLetter(character) && !unicode.IsNumber(character) && character != '-' {
			return fmt.Errorf("%w: name contains unsupported characters", ErrInvalid)
		}
	}
	if strings.ContainsAny(name, "/\\:\x00") {
		return fmt.Errorf("%w: name cannot contain path separators", ErrInvalid)
	}
	return nil
}

func validateDescription(description string) error {
	if !utf8.ValidString(description) || strings.IndexByte(description, 0) >= 0 || strings.TrimSpace(description) == "" || utf8.RuneCountInString(description) > 1024 {
		return fmt.Errorf("%w: description must contain 1-1024 characters", ErrInvalid)
	}
	return nil
}
