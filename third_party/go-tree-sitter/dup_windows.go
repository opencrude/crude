//go:build windows

package tree_sitter

/*
#include <stdint.h>
#include <windows.h>

int _ts_dup(HANDLE handle);

// os.File.Fd returns a Windows HANDLE, not a Go pointer or a CRT descriptor.
// Keep the native integer-to-handle conversion on the C side of the boundary.
static int ts_dup_file_handle(uintptr_t handle) {
	return _ts_dup((HANDLE)handle);
}
*/
import "C"

// Wrapper for Windows systems
func dupeFD(handle uintptr) int {
	return int(C.ts_dup_file_handle(C.uintptr_t(handle)))
}
