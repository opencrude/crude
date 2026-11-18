package projectpath

import "path/filepath"

func personalDirectories(home string) ([]string, error) {
	if home == "" {
		return nil, nil
	}
	var result []string
	for _, name := range []string{"Desktop", "Documents", "Downloads", "Music", "Pictures", "Movies", "Public"} {
		result = append(result, filepath.Join(home, name))
	}
	return result, nil
}
