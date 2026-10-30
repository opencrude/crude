package lsp

import (
	"os"
	"path/filepath"
	"strings"
)

func processEnvironmentKey(key string) string { return strings.ToUpper(key) }

// Only EXE/COM are native launch candidates. Other syntactically valid explicit
// PATHEXT entries remain launch data but cannot cause shell/script interpretation.
// No default extensions are supplied when PATHEXT is absent.
func prepareProcessExtensions(value string) ([]string, error) {
	if strings.Count(value, ";") >= 16 {
		return nil, ErrProcessEnvironment
	}
	var out []string
	seen := make(map[string]bool)
	for _, extension := range strings.Split(value, ";") {
		if len(extension) < 2 || len(extension) > 32 || extension[0] != '.' {
			return nil, ErrProcessEnvironment
		}
		for i := 1; i < len(extension); i++ {
			c := extension[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
				continue
			}
			return nil, ErrProcessEnvironment
		}
		extension = strings.ToLower(extension)
		if (extension == ".exe" || extension == ".com") && !seen[extension] {
			out = append(out, extension)
			seen[extension] = true
		}
	}
	return out, nil
}

func validProcessAbsolutePath(path string) bool {
	if !validProcessPathText(path) || !filepath.IsAbs(path) {
		return false
	}
	path = strings.ReplaceAll(path, "/", `\`)
	// Refuse device/extended namespaces and alternate data streams. Only ordinary
	// drive-absolute and UNC paths are admitted, never drive-relative/rooted paths.
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return false
	}
	volume := filepath.VolumeName(path)
	if strings.HasPrefix(volume, `\\`) {
		parts := strings.Split(volume[2:], `\`)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return false
		}
	} else if len(volume) != 2 || !isProcessDriveLetter(volume[0]) || volume[1] != ':' {
		return false
	}
	for i := 0; i < len(path); i++ {
		if strings.ContainsRune(`<>"|?*`, rune(path[i])) ||
			path[i] == ':' && (i != 1 || !isProcessDriveLetter(path[0])) {
			return false
		}
	}
	for _, part := range strings.Split(path, `\`) {
		if part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}

func isProcessDriveLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func processExplicitExecutable(path string) bool {
	extension := filepath.Ext(path)
	return strings.EqualFold(extension, ".exe") || strings.EqualFold(extension, ".com")
}

func processExecutableNames(command string, extensions []string) []string {
	if !validProcessAbsolutePath(`C:\` + command) {
		return nil
	}
	if filepath.Ext(command) != "" {
		if processExplicitExecutable(command) {
			return []string{command}
		}
		return nil
	}
	out := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		out = append(out, command+extension)
	}
	return out
}

func processExecutableMode(os.FileMode) bool { return true }
