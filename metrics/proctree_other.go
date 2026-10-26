//go:build !linux && !windows

package metrics

import (
	"context"
	"fmt"
	gproc "github.com/shirou/gopsutil/v4/process"
)

// processParents asks the OS for each process's parent.
func processParents(ctx context.Context) (map[int32]int32, error) {
	pids, err := gproc.PidsWithContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int32]int32, len(pids))
	unreadable := 0
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if parent, err := (&gproc.Process{Pid: pid}).PpidWithContext(ctx); err == nil {
			out[pid] = parent
		} else {
			unreadable++
		}
	}
	if unreadable > 0 {
		return out, fmt.Errorf("process parent enumeration partial: %d unreadable entries", unreadable)
	}
	return out, nil
}
