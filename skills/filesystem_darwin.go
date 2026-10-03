package skills

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func nonblockingReadFlag() int { return unix.O_NONBLOCK }

func publishDirectory(parent *os.Root, from, to string) error {
	directory, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := unix.RenameatxNp(int(directory.Fd()), from, int(directory.Fd()), to, unix.RENAME_EXCL); err != nil {
		return fmt.Errorf("publish skill: %w", err)
	}
	return nil
}
