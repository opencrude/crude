package fileio

import (
	"context"
	"os"
)

var mutationLease = func() chan struct{} {
	lease := make(chan struct{}, 1)
	lease <- struct{}{}
	return lease
}()

func WriteFile(path string, data []byte, perm os.FileMode) error {
	unlock := LockFileMutation(path)
	defer unlock()
	return os.WriteFile(path, data, perm)
}

func LockMutationContext(ctx context.Context) (func(), error) {
	select {
	case <-mutationLease:
		return func() { mutationLease <- struct{}{} }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TryLockMutation() (func(), bool) {
	select {
	case <-mutationLease:
		return func() { mutationLease <- struct{}{} }, true
	default:
		return nil, false
	}
}

// LockFileMutation serializes Crude-owned mutations through the same lease.
func LockFileMutation(string) func() {
	unlock, _ := LockMutationContext(context.Background())
	return unlock
}

// LockTreeMutation uses the same authority as individual file commits.
func LockTreeMutation() func() {
	return LockFileMutation("")
}
