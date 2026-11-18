//go:build windows

package sessiondb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
	"modernc.org/libc"
	"modernc.org/memory"
)

func closeNativeAdapterHandle(t *testing.T, handle windows.Handle) {
	t.Helper()
	if err := windows.CloseHandle(handle); err != nil {
		t.Error("close native adapter handle:", err)
	}
}

func nativeAdapterErrno(tls *libc.TLS) uint32 {
	// Borrow the module's typed byte view of its C-owned errno storage.
	// The owning TLS remains live until the test's deferred Close.
	return binary.LittleEndian.Uint32(libc.GoBytes(libc.X_errno(tls), 4))
}

func TestNativeFileAdapters(t *testing.T) {
	tls := libc.NewTLS()
	defer tls.Close()
	name, err := windows.UTF16PtrFromString(filepath.Join(t.TempDir(), "雪 & native #.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := libc.XCreateFileW(tls, uintptr(unsafe.Pointer(name)), windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, 0, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if windows.Handle(h) == windows.InvalidHandle {
		t.Fatal("CreateFileW failed")
	}
	defer closeNativeAdapterHandle(t, windows.Handle(h))
	want := []byte("native adapter bytes \x00 \xff")
	var count uint32
	if libc.XWriteFile(tls, h, uintptr(unsafe.Pointer(&want[0])), uint32(len(want)), uintptr(unsafe.Pointer(&count)), 0) == 0 || count != uint32(len(want)) {
		t.Fatalf("write failed: count %d", count)
	}
	var attrs windows.Win32FileAttributeData
	if libc.XGetFileAttributesExW(tls, uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&attrs))) == 0 || attrs.FileSizeLow != uint32(len(want)) || attrs.FileSizeHigh != 0 {
		t.Fatalf("attributes: %+v", attrs)
	}
	var high uint32
	if size := libc.XGetFileSize(tls, h, uintptr(unsafe.Pointer(&high))); size != uint32(len(want)) || high != 0 {
		t.Fatalf("size %d:%d", high, size)
	}
	if _, err := windows.SetFilePointer(windows.Handle(h), 0, nil, windows.FILE_BEGIN); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	count = 0
	runtime.GC()
	if libc.XReadFile(tls, h, uintptr(unsafe.Pointer(&got[0])), uint32(len(got)), uintptr(unsafe.Pointer(&count)), 0) == 0 || count != uint32(len(want)) || !bytes.Equal(got, want) {
		t.Fatalf("read count %d: %x", count, got)
	}
	binary.LittleEndian.PutUint32(libc.GoBytes(libc.X_errno(tls), 4), 0)
	if libc.XReadFile(tls, uintptr(windows.InvalidHandle), uintptr(unsafe.Pointer(&got[0])), 1, uintptr(unsafe.Pointer(&count)), 0) != 0 {
		t.Fatal("invalid handle read succeeded")
	}
	if errno := nativeAdapterErrno(tls); errno != uint32(windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("invalid handle errno %d", errno)
	}
}

func TestNativeConversionAndMemory(t *testing.T) {
	tls := libc.NewTLS()
	defer tls.Close()
	text := []byte("雪x")
	var wide [4]uint16
	n := libc.XMultiByteToWideChar(tls, 65001, 0, uintptr(unsafe.Pointer(&text[0])), int32(len(text)), uintptr(unsafe.Pointer(&wide[0])), int32(len(wide)))
	if n != 2 || wide[0] != 0x96ea || wide[1] != 'x' {
		t.Fatalf("conversion: %d %x", n, wide)
	}
	var allocator memory.Allocator
	b, err := allocator.Malloc(128 * 1024)
	if err != nil || len(b) != 128*1024 {
		t.Fatalf("allocate: %d %v", len(b), err)
	}
	b[0], b[len(b)-1] = 0x19, 0xa7
	if b[0] != 0x19 || b[len(b)-1] != 0xa7 {
		t.Fatal("mapped memory content")
	}
	if err := allocator.Free(b); err != nil {
		t.Fatalf("free: %v", err)
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		huge := int64(1) << 62
		b, err = allocator.Malloc(int(huge))
		if err == nil {
			_ = allocator.Free(b)
			t.Fatal("impossible allocation succeeded")
		}
		if _, ok := err.(windows.Errno); !ok {
			t.Fatalf("allocation error type %T, want Windows errno", err)
		}
	}
}

func TestNativePendingReadOwnership(t *testing.T) {
	tls := libc.NewTLS()
	defer tls.Close()
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\crude-native-%d-%d`, os.Getpid(), tls.ID))
	if err != nil {
		t.Fatal(err)
	}
	server, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_OVERLAPPED, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNativeAdapterHandle(t, server)
	client, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNativeAdapterHandle(t, client)
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNativeAdapterHandle(t, event)
	buf := make([]byte, 8)
	overlap := &windows.Overlapped{HEvent: event}
	var pin runtime.Pinner
	pin.Pin(&buf[0])
	pin.Pin(overlap)
	defer pin.Unpin()
	pending := false
	defer func() {
		if pending {
			_ = windows.CancelIoEx(server, overlap)
			var done uint32
			_ = windows.GetOverlappedResult(server, overlap, &done, true)
		}
	}()
	if libc.XReadFile(tls, uintptr(server), uintptr(unsafe.Pointer(&buf[0])), uint32(len(buf)), 0, uintptr(unsafe.Pointer(overlap))) != 0 {
		t.Fatal("empty pipe read unexpectedly completed")
	}
	pending = true
	if errno := nativeAdapterErrno(tls); errno != uint32(windows.ERROR_IO_PENDING) {
		t.Fatalf("pending read errno %d", errno)
	}
	runtime.GC()
	want := []byte("pending!")
	var written uint32
	if err := windows.WriteFile(client, want, &written, nil); err != nil || written != uint32(len(want)) {
		t.Fatalf("pipe write %d: %v", written, err)
	}
	if status, err := windows.WaitForSingleObject(event, 5_000); err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("pending read did not complete: status=%d, error=%v", status, err)
	}
	var done uint32
	err = windows.GetOverlappedResult(server, overlap, &done, true)
	pending = false
	if err != nil || done != uint32(len(want)) || !bytes.Equal(buf, want) {
		t.Fatalf("completion %d %x: %v", done, buf, err)
	}
}

func TestNativeFileLockAndAllocations(t *testing.T) {
	tls := libc.NewTLS()
	defer tls.Close()
	name, err := windows.UTF16PtrFromString(filepath.Join(t.TempDir(), "locks.db"))
	if err != nil {
		t.Fatal(err)
	}
	open := func(disposition uint32) uintptr {
		return libc.XCreateFileW(tls, uintptr(unsafe.Pointer(name)), windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, 0, disposition, windows.FILE_ATTRIBUTE_NORMAL, 0)
	}
	h1 := open(windows.CREATE_NEW)
	if windows.Handle(h1) == windows.InvalidHandle {
		t.Fatal("create failed")
	}
	defer closeNativeAdapterHandle(t, windows.Handle(h1))
	h2 := open(windows.OPEN_EXISTING)
	if windows.Handle(h2) == windows.InvalidHandle {
		t.Fatal("second open failed")
	}
	defer closeNativeAdapterHandle(t, windows.Handle(h2))
	var a, b windows.Overlapped
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	if libc.XLockFileEx(tls, h1, flags, 0, 1, 0, uintptr(unsafe.Pointer(&a))) == 0 {
		t.Fatal("first lock failed")
	}
	if libc.XLockFileEx(tls, h2, flags, 0, 1, 0, uintptr(unsafe.Pointer(&b))) != 0 {
		t.Fatal("conflicting lock succeeded")
	}
	if errno := nativeAdapterErrno(tls); errno != uint32(windows.ERROR_LOCK_VIOLATION) {
		t.Fatalf("conflicting lock errno %d", errno)
	}
	if libc.XUnlockFileEx(tls, h1, 0, 1, 0, uintptr(unsafe.Pointer(&a))) == 0 || libc.XLockFileEx(tls, h2, flags, 0, 1, 0, uintptr(unsafe.Pointer(&b))) == 0 || libc.XUnlockFileEx(tls, h2, 0, 1, 0, uintptr(unsafe.Pointer(&b))) == 0 {
		t.Fatal("unlock/reacquire failed")
	}
	data := []byte{42}
	var read [1]byte
	var count uint32
	// The setup and byte-count storage are outside the measured calls. The
	// old LazyProc.Call allocates a variadic argument slice on each call.
	allocs := testing.AllocsPerRun(100, func() {
		if _, err := windows.SetFilePointer(windows.Handle(h1), 0, nil, windows.FILE_BEGIN); err != nil {
			panic(err)
		}
		if libc.XWriteFile(tls, h1, uintptr(unsafe.Pointer(&data[0])), 1, uintptr(unsafe.Pointer(&count)), 0) == 0 || count != 1 {
			panic("write did not preserve the byte count")
		}
		if _, err := windows.SetFilePointer(windows.Handle(h1), 0, nil, windows.FILE_BEGIN); err != nil {
			panic(err)
		}
		if libc.XReadFile(tls, h1, uintptr(unsafe.Pointer(&read[0])), 1, uintptr(unsafe.Pointer(&count)), 0) == 0 ||
			count != 1 || read[0] != 42 {
			panic("read did not return the actual file byte")
		}
	})
	if allocs != 0 {
		t.Fatalf("read/write allocated %g times per pair", allocs)
	}
}
