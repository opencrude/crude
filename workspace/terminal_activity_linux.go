package workspace

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func processName(pid int) string {
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}

func backgroundProgram(shell int) (string, bool, error) {
	root := "/proc/" + strconv.Itoa(shell) + "/task"
	tasks, err := os.ReadDir(root)
	if err != nil {
		return "", false, err
	}
	for _, task := range tasks {
		if !task.IsDir() {
			continue
		}
		children, err := os.ReadFile(root + "/" + task.Name() + "/children")
		if os.IsNotExist(err) { // the shell thread ended during observation
			continue
		}
		if err != nil {
			return "", false, err
		}
		for _, pid := range strings.Fields(string(children)) {
			stat, err := os.ReadFile("/proc/" + pid + "/stat")
			if os.IsNotExist(err) {
				continue // this child was reaped
			}
			if err != nil {
				return "", false, err
			}
			raw := string(stat)
			start, end := strings.IndexByte(raw, '('), strings.LastIndex(raw, ") ")
			if start < 0 || end <= start {
				return "", false, fmt.Errorf("cannot read terminal child %s", pid)
			}
			fields := strings.Fields(raw[end+2:])
			if len(fields) < 2 {
				return "", false, fmt.Errorf("cannot read terminal child %s", pid)
			}
			if fields[0] == "Z" || fields[1] != strconv.Itoa(shell) {
				continue // exited child, or the PID was already reused
			}
			return raw[start+1 : end], true, nil
		}
	}
	return "", false, nil
}
