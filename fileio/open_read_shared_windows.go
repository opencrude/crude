package fileio

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// OpenReadSharedDelete permits a concurrent rename or deletion during a read.
// Callers that need a stable source must verify its identity after reading.
func OpenReadSharedDelete(path string) (*os.File, error) {
	return openReadSharedDelete(path, windows.FILE_ATTRIBUTE_NORMAL)
}

// ReadFileSharedDelete reads a whole file without blocking a concurrent
// rename or deletion. Background readers (search, the text index) use it so
// an atomic save, which renames a new file over the old one, never fails
// because Crude was reading the old one.
func ReadFileSharedDelete(path string) ([]byte, error) {
	f, err := OpenReadSharedDelete(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	// Size the buffer from the file, as os.ReadFile does, so a dense
	// search does not grow it step by step.
	size := 512
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info) == nil {
		if length := int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow); length > 0 && length < 1<<30 {
			size = int(length) + 1
		}
	}
	data := make([]byte, 0, size)
	for {
		n, err := f.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if err == io.EOF {
			return data, nil
		}
		if err != nil {
			return data, err
		}
		if len(data) == cap(data) {
			data = append(data, 0)[:len(data)]
		}
	}
}

// OpenDirectorySharedDelete permits a directory scan to coexist with renames
// and deletion. Windows requires backup semantics to open a directory.
func OpenDirectorySharedDelete(path string) (*os.File, error) {
	return openReadSharedDelete(path, windows.FILE_FLAG_BACKUP_SEMANTICS)
}

func openReadSharedDelete(path string, flags uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}
