//go:build !windows

package toolstore

import "os"

func sealFile(name string, executable bool) error {
	if executable {
		return os.Chmod(name, 0o555)
	}
	return os.Chmod(name, 0o444)
}

func sealDir(name string) error { return os.Chmod(name, 0o555) }

func unsealFile(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	return os.Chmod(name, info.Mode().Perm()|0o600)
}

func unsealDir(name string) error { return os.Chmod(name, 0o700) }

// syncFile and syncDir persist the named inode; for directories this also
// persists its child entries.
func syncFile(name string) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func syncDir(name string) error { return syncFile(name) }
