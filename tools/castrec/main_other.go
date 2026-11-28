//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "castrec needs Windows: it records through the Windows pseudo console.")
	os.Exit(1)
}
