package toolstore

import (
	"io/fs"
	"os"
	"path/filepath"
)

// sealTree makes every file and directory below dir read-only, children
// before their parents, then syncs their metadata and directory entries. dir
// itself is not made read-only. Links are not followed.
func sealTree(dir string) error {
	type item struct {
		path string
		mode fs.FileMode
	}
	var items []item
	err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == dir {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		items = append(items, item{path: name, mode: info.Mode()})
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(items) - 1; i >= 0; i-- {
		switch {
		case items[i].mode.IsDir():
			if err := sealDir(items[i].path); err != nil {
				return err
			}
			if err := syncDir(items[i].path); err != nil {
				return err
			}
		case items[i].mode.IsRegular():
			if err := sealFile(items[i].path, items[i].mode&0o111 != 0); err != nil {
				return err
			}
			if err := syncFile(items[i].path); err != nil {
				return err
			}
		}
	}
	return syncDir(dir)
}

// unsealTree restores write permission on dir and everything below it,
// parents before their children. Links are not followed, so nothing outside
// dir changes.
func unsealTree(dir string) error {
	return filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			if err := unsealDir(name); err != nil {
				return err
			}
		case entry.Type().IsRegular():
			if err := unsealFile(name); err != nil {
				return err
			}
		}
		return nil
	})
}

// removeTree deletes a tree that the store owns, after it restores write
// permission inside it.
func removeTree(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := unsealTree(dir); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}
