package skills

import (
	"errors"
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
	if err := unix.Renameat2(int(directory.Fd()), from, int(directory.Fd()), to, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
			return fmt.Errorf("%w: filesystem cannot publish skill directories without replacement", ErrUnsupported)
		}
		return fmt.Errorf("publish skill: %w", err)
	}
	return nil
}
