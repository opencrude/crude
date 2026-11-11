package workspace

import (
	"bytes"

	"golang.org/x/sys/unix"
)

func processName(pid int) string {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ""
	}
	name := info.Proc.P_comm[:]
	if end := bytes.IndexByte(name, 0); end >= 0 {
		name = name[:end]
	}
	return string(name)
}

func backgroundProgram(shell int) (string, bool, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return "", false, err
	}
	for _, process := range processes {
		if int(process.Eproc.Ppid) == shell && process.Proc.P_stat != 5 { // SZOMB
			return processName(int(process.Proc.P_pid)), true, nil
		}
	}
	return "", false, nil
}
