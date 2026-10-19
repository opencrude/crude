package testhome

import (
	"os"
	"path/filepath"
)

// Go inherits an 8.3 TEMP on hosted Windows runners, while Git and core
// attachment return long paths. Give fixtures one spelling of the same root;
// dedicated short-name regressions exercise aliases explicitly.
func normalizeTempDir() error {
	path, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return err
	}
	for _, key := range []string{"TMP", "TEMP"} {
		if err := os.Setenv(key, path); err != nil {
			return err
		}
	}
	return nil
}
