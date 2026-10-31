//go:build !windows

package workspace

import (
	"os"
	"syscall"
)

func openProjectArtifact(root *os.Root, path string) (*os.File, error) {
	// A FIFO replacing the preflight regular file must not wedge the worker.
	// fstat still rejects that object; nonblocking is not a regular-file deadline.
	return root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
