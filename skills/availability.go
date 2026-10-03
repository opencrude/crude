package skills

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

var executableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.+-]*$`)

type RequirementError struct {
	Requirement string
	Reason      string
}

func (err *RequirementError) Error() string {
	return fmt.Sprintf("%s: %s: %s", ErrUnavailable, err.Requirement, err.Reason)
}

func (err *RequirementError) Unwrap() error { return ErrUnavailable }

func CheckAvailability(skill Skill) error {
	var failures []error
	for _, key := range []string{"crude.os", "crude.arch", "crude.product"} {
		declaration, exists := skill.Metadata[key]
		if !exists {
			continue
		}
		actual := runtime.GOOS
		switch key {
		case "crude.arch":
			actual = runtime.GOARCH
		case "crude.product":
			actual = "crude"
		}
		values := requirementWords(declaration)
		if len(values) == 0 || !slices.Contains(values, actual) {
			failures = append(failures, &RequirementError{Requirement: key, Reason: "not compatible with " + actual})
		}
	}
	for _, key := range []string{"crude.requires", "requires", "dependencies"} {
		declaration, exists := skill.Metadata[key]
		if !exists {
			continue
		}
		if err := checkExecutables(key, declaration); err != nil {
			failures = append(failures, err)
		}
	}
	if skill.Compatibility != "" {
		declaration := strings.TrimSpace(skill.Compatibility)
		switch {
		case strings.EqualFold(declaration, "crude"), strings.EqualFold(declaration, "Designed for Crude"):
		case declaration == "linux" || declaration == "darwin" || declaration == "windows":
			if declaration != runtime.GOOS {
				failures = append(failures, &RequirementError{Requirement: "compatibility", Reason: "requires " + declaration + "; running " + runtime.GOOS})
			}
		case strings.HasPrefix(declaration, "Requires "):
			if err := checkExecutables("compatibility", strings.TrimPrefix(declaration, "Requires ")); err != nil {
				failures = append(failures, err)
			}
		default:
			failures = append(failures, &RequirementError{Requirement: "compatibility", Reason: "free-form requirements cannot be verified automatically; inspect before use"})
		}
	}
	return errors.Join(failures...)
}

func requirementWords(declaration string) []string {
	return strings.FieldsFunc(declaration, func(character rune) bool {
		return character == ',' || character == ' ' || character == '\n' || character == '\r' || character == '\t'
	})
}

func checkExecutables(field, declaration string) error {
	words := requirementWords(declaration)
	if len(words) == 0 {
		return &RequirementError{Requirement: field, Reason: "dependency declaration is empty"}
	}
	var failures []error
	declared := false
	for _, executable := range words {
		if executable == "and" {
			continue
		}
		declared = true
		if !executableNamePattern.MatchString(executable) || strings.Contains(executable, "+") {
			failures = append(failures, &RequirementError{Requirement: field, Reason: "only executable names can be verified; version, path, network, and shell expressions are unsupported"})
			continue
		}
		if _, err := exec.LookPath(executable); err != nil {
			failures = append(failures, &RequirementError{Requirement: executable, Reason: "required executable is not available on PATH"})
		}
	}
	if !declared {
		return &RequirementError{Requirement: field, Reason: "dependency declaration has no executable names"}
	}
	return errors.Join(failures...)
}
