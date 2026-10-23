package metrics

import (
	"bytes"
	"os"
	"strconv"
)

// processParents reads every process's parent from /proc in one pass.
func processParents() (map[int32]int32, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	out := make(map[int32]int32, len(entries))
	for _, entry := range entries {
		pid, err := strconv.ParseInt(entry.Name(), 10, 32)
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		// "pid (comm) state ppid ...": comm may contain spaces and parens.
		end := bytes.LastIndexByte(stat, ')')
		if end < 0 {
			continue
		}
		fields := bytes.Fields(stat[end+1:])
		if len(fields) < 2 {
			continue
		}
		if parent, err := strconv.ParseInt(string(fields[1]), 10, 32); err == nil {
			out[int32(pid)] = int32(parent)
		}
	}
	return out, nil
}
