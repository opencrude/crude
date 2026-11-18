package skills

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func nonblockingReadFlag() int { return 0 }

type fileRenameInformation struct {
	ReplaceIfExists bool
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [256]uint16
}

func publishDirectory(parent *os.Root, from, to string) error {
	directory, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	objectName, err := windows.NewNTUnicodeString(from)
	if err != nil {
		return err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(directory.Fd()),
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var source windows.Handle
	var status windows.IO_STATUS_BLOCK
	if err := windows.NtCreateFile(&source, windows.SYNCHRONIZE|windows.DELETE, &attributes, &status, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0); err != nil {
		return fmt.Errorf("open skill publication directory: %w", windowsError(err))
	}
	sourceFile := os.NewFile(uintptr(source), from)
	defer sourceFile.Close()
	filename, err := windows.UTF16FromString(to)
	if err != nil {
		return err
	}
	rename := fileRenameInformation{RootDirectory: windows.Handle(directory.Fd())}
	if len(filename) > len(rename.FileName) {
		return fmt.Errorf("%w: destination filename is too long", ErrInvalid)
	}
	copy(rename.FileName[:], filename)
	rename.FileNameLength = uint32((len(filename) - 1) * 2)
	if err := windows.NtSetInformationFile(source, &status, (*byte)(unsafe.Pointer(&rename)), uint32(unsafe.Sizeof(rename)), windows.FileRenameInformation); err != nil {
		return fmt.Errorf("publish skill: %w", windowsError(err))
	}
	return nil
}

func windowsError(err error) error {
	var status windows.NTStatus
	if errors.As(err, &status) {
		return status.Errno()
	}
	return err
}
