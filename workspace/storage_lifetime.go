package workspace

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// Catalog readers borrow saved databases outside their workspace actor. Moves
// exclude those readers until their real SQL handles close, including workers
// unwinding after a catalog timeout. Concurrent metadata reads stay concurrent.
const workspaceStorageCapacity = 1 << 30

var workspaceStorage = semaphore.NewWeighted(workspaceStorageCapacity)

func lockWorkspaceStorage(ctx context.Context, move bool) (func(), error) {
	weight := int64(1)
	if move {
		weight = workspaceStorageCapacity
	}
	if err := workspaceStorage.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	return func() { workspaceStorage.Release(weight) }, nil
}
