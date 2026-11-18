package fileio

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type nativeDirectoryIdentityHandle windows.Handle

func openDirectoryIdentityRoot(root string) (directoryIdentityHandle, error) {
	name, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, fmt.Errorf("open directory volume root: %w", err)
	}
	return nativeDirectoryIdentityHandle(handle), nil
}

func (h nativeDirectoryIdentityHandle) openChild(name string) (directoryIdentityHandle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(h),
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE, &attributes, &status,
		nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("open no-reparse directory relative to retained parent: %w", err)
	}
	return nativeDirectoryIdentityHandle(handle), nil
}

func (h nativeDirectoryIdentityHandle) close() error {
	return windows.CloseHandle(windows.Handle(h))
}

// Matches FILE_ID_INFO, including its 64-bit volume ID and full 128-bit file ID.
type directoryFileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

func (h nativeDirectoryIdentityHandle) identity(withBirth bool) (string, error) {
	var basic windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(h), &basic); err != nil {
		return "", fmt.Errorf("read directory attributes/creation time: %w", err)
	}
	if basic.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || basic.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return "", fmt.Errorf("directory identity rejects reparse points and non-directory handles")
	}
	var id directoryFileIDInfo
	if err := windows.GetFileInformationByHandleEx(windows.Handle(h), windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id))); err != nil {
		return "", fmt.Errorf("directory identity unavailable: filesystem must support FileIdInfo native volume/file IDs: %w", err)
	}
	if id.VolumeSerialNumber == 0 || id.FileID == [16]byte{} {
		return "", fmt.Errorf("directory identity unavailable: filesystem returned an empty volume/file ID")
	}
	object := fmt.Sprintf("windows:v1:%x:%x", id.VolumeSerialNumber, id.FileID)
	if !withBirth {
		return object, nil
	}
	creation := uint64(basic.CreationTime.HighDateTime)<<32 | uint64(basic.CreationTime.LowDateTime)
	if creation == 0 {
		return "", fmt.Errorf("directory identity unavailable: filesystem must provide trustworthy creation time")
	}
	return fmt.Sprintf("%s:%x", object, creation), nil
}
