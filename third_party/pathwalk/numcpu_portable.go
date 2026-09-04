//go:build !(darwin && arm64)

package pathwalk

func darwinNumPerfCores() int {
	return -1
}
