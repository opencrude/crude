//go:build !linux && !windows

package metrics

import gproc "github.com/shirou/gopsutil/v4/process"

// processParents asks the OS for each process's parent.
func processParents() (map[int32]int32, error) {
	pids, err := gproc.Pids()
	if err != nil {
		return nil, err
	}
	out := make(map[int32]int32, len(pids))
	for _, pid := range pids {
		if parent, err := (&gproc.Process{Pid: pid}).Ppid(); err == nil {
			out[pid] = parent
		}
	}
	return out, nil
}
