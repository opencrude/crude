package metrics

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processParents reads every process's parent from one Toolhelp snapshot.
func processParents() (map[int32]int32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	out := make(map[int32]int32)
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		out[int32(entry.ProcessID)] = int32(entry.ParentProcessID)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return out, err
	}
	return out, nil
}
