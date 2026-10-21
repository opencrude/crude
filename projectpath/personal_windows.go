package projectpath

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func personalDirectories(_ string) ([]string, error) {
	var result []string
	var issues []error
	for _, folder := range []struct {
		name string
		id   *windows.KNOWNFOLDERID
	}{
		{"Profile", windows.FOLDERID_Profile}, {"Desktop", windows.FOLDERID_Desktop},
		{"Documents", windows.FOLDERID_Documents}, {"Downloads", windows.FOLDERID_Downloads},
		{"Music", windows.FOLDERID_Music}, {"Pictures", windows.FOLDERID_Pictures},
		{"Videos", windows.FOLDERID_Videos}, {"Public", windows.FOLDERID_Public},
		{"Templates", windows.FOLDERID_Templates},
	} {
		// DONT_VERIFY obtains configured redirected paths without requiring that
		// optional folders exist or making the API create any directories.
		path, err := windows.KnownFolderPath(folder.id, windows.KF_FLAG_DONT_VERIFY)
		if err != nil {
			issues = append(issues, fmt.Errorf("locate Windows personal folder %s: %w", folder.name, err))
			continue
		}
		result = append(result, path)
	}
	return result, errors.Join(issues...)
}
