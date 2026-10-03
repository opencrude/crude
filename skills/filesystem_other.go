//go:build !linux && !darwin && !windows

package skills

import (
	"fmt"
	"os"
)

func nonblockingReadFlag() int { return 0 }

func publishDirectory(*os.Root, string, string) error {
	return fmt.Errorf("%w: atomic no-replace skill creation is not supported on this platform", ErrUnsupported)
}
