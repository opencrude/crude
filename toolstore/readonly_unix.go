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

// syncDir makes a rename inside dir durable.
func syncDir(name string) error {
	dir, err := os.Open(name)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
