//go:build windows

package desktopnotify

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The few Windows Runtime calls that toasts need. Interface IDs and method
// slots come from the Windows SDK headers winrt/windows.ui.notifications.h
// and winrt/windows.data.xml.dom.h (10.0.26100.0). Slots 0-2 are IUnknown
// and 3-5 are IInspectable, so an interface's first own method is slot 6.
// Each call passes its pointers inline, as the unsafe.Pointer rules require
// for system calls.

var (
	combase                       = windows.NewLazySystemDLL("combase.dll")
	procRoInitialize              = combase.NewProc("RoInitialize")
	procRoUninitialize            = combase.NewProc("RoUninitialize")
	procRoActivateInstance        = combase.NewProc("RoActivateInstance")
	procRoGetActivationFactory    = combase.NewProc("RoGetActivationFactory")
	procWindowsCreateString       = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString       = combase.NewProc("WindowsDeleteString")
	procWindowsGetStringRawBuffer = combase.NewProc("WindowsGetStringRawBuffer")
)

var (
	iidUnknown                       = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidAgileObject                   = windows.GUID{Data1: 0x94ea2b94, Data2: 0xe9cc, Data3: 0x49e0, Data4: [8]byte{0xc0, 0xff, 0xee, 0x64, 0xca, 0x8f, 0x5b, 0x90}}
	iidToastNotificationManagerStats = mustGUID("{50ac103f-d235-4598-bbef-98fe4d1a3ad4}")
	iidToastNotificationManagerStat2 = mustGUID("{7ab93c52-0e48-4750-ba9d-1a4113981847}")
	iidToastNotificationFactory      = mustGUID("{04124b20-82c6-4229-b109-fd9ed4662b53}")
	iidToastNotification2            = mustGUID("{9dfb9fd1-143a-490e-90bf-b9fba7132de7}")
	iidXMLDocument                   = mustGUID("{f7f3a506-1e87-42d6-bcfb-b8c809fa5494}")
	iidXMLDocumentIO                 = mustGUID("{6cd0e74e-ee65-4489-9ebf-ca43e87ba637}")
	// TypedEventHandler<ToastNotification, IInspectable>.
	iidToastActivatedHandler = mustGUID("{ab54de2d-97d9-5528-b6ad-105afe156530}")
)

const (
	classToastNotificationManager = "Windows.UI.Notifications.ToastNotificationManager"
	classToastNotification        = "Windows.UI.Notifications.ToastNotification"
	classXMLDocument              = "Windows.Data.Xml.Dom.XmlDocument"

	roInitMultithreaded = 1
	sFalse              = 1
	rpcEChangedMode     = 0x80010106
	eNoInterface        = 0x80004002
)

// Method slots, by interface.
const (
	slotQueryInterface = 0
	slotRelease        = 2

	slotManagerCreateToastNotifierWithID = 7 // IToastNotificationManagerStatics
	slotManagerGetHistory                = 6 // IToastNotificationManagerStatics2
	slotFactoryCreateToastNotification   = 6 // IToastNotificationFactory
	slotToastAddActivated                = 11
	slotToastRemoveActivated             = 12
	slotToast2PutTag                     = 6
	slotToast2GetTag                     = 7
	slotToast2PutGroup                   = 8
	slotToast2PutSuppressPopup           = 10
	slotNotifierShow                     = 6
	slotNotifierGetSetting               = 8
	slotHistoryRemoveGroupedTagWithID    = 8
	slotXMLDocumentIOLoadXML             = 6
)

func mustGUID(text string) windows.GUID {
	guid, err := windows.GUIDFromString(text)
	if err != nil {
		panic(err)
	}
	return guid
}

// comObject is a Windows Runtime interface pointer. It points at memory the
// Windows Runtime owns; Go never allocates or frees one.
type comObject struct{ vtbl *[64]uintptr }

func (o *comObject) self() uintptr { return uintptr(unsafe.Pointer(o)) }

func (o *comObject) release() {
	if o != nil {
		_, _, _ = syscall.SyscallN(o.vtbl[slotRelease], o.self())
	}
}

func (o *comObject) query(iid *windows.GUID) (*comObject, error) {
	var out *comObject
	hr, _, _ := syscall.SyscallN(o.vtbl[slotQueryInterface], o.self(), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	return out, check("QueryInterface", hr)
}

type hresultError struct {
	op string
	hr uint32
}

func (e hresultError) Error() string { return fmt.Sprintf("%s failed with HRESULT 0x%08X", e.op, e.hr) }

func check(op string, hr uintptr) error {
	if int32(hr) < 0 {
		return hresultError{op: op, hr: uint32(hr)}
	}
	return nil
}

// hstring is a Windows Runtime string handle. The caller deletes the
// handles it creates and the ones a method returns.
type hstring uintptr

func newHString(text string) (hstring, error) {
	units, err := windows.UTF16FromString(text)
	if err != nil {
		return 0, err
	}
	var h hstring
	// The length counts UTF-16 units without the terminator, so text
	// outside the Basic Multilingual Plane keeps its full length.
	hr, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(&units[0])), uintptr(len(units)-1), uintptr(unsafe.Pointer(&h)))
	return h, check("WindowsCreateString", hr)
}

func (h hstring) free() {
	if h != 0 {
		_, _, _ = procWindowsDeleteString.Call(uintptr(h))
	}
}

func (h hstring) String() string {
	if h == 0 {
		return ""
	}
	var length uint32
	raw, _, _ := procWindowsGetStringRawBuffer.Call(uintptr(h), uintptr(unsafe.Pointer(&length)))
	if raw == 0 || length == 0 {
		return ""
	}
	buffer := *(**uint16)(unsafe.Pointer(&raw))
	return windows.UTF16ToString(unsafe.Slice(buffer, length))
}

func activationFactory(class string, iid *windows.GUID) (*comObject, error) {
	name, err := newHString(class)
	if err != nil {
		return nil, err
	}
	defer name.free()
	var out *comObject
	hr, _, _ := procRoGetActivationFactory.Call(uintptr(name), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	return out, check("RoGetActivationFactory "+class, hr)
}

func activateInstance(class string) (*comObject, error) {
	name, err := newHString(class)
	if err != nil {
		return nil, err
	}
	defer name.free()
	var out *comObject
	hr, _, _ := procRoActivateInstance.Call(uintptr(name), uintptr(unsafe.Pointer(&out)))
	return out, check("RoActivateInstance "+class, hr)
}

// roInitialize joins this thread to the multithreaded apartment. It reports
// whether the caller must call RoUninitialize on the same thread later.
func roInitialize() (bool, error) {
	hr, _, _ := procRoInitialize.Call(roInitMultithreaded)
	switch {
	case hr == 0 || hr == sFalse:
		return true, nil
	case uint32(hr) == rpcEChangedMode:
		// Another apartment owns this thread already; it stays usable.
		return false, nil
	}
	return false, check("RoInitialize", hr)
}

func roUninitialize() { _, _, _ = procRoUninitialize.Call() }

// activatedHandler is the one TypedEventHandler<ToastNotification,
// IInspectable> of this process. Windows may call it after Crude releases a
// toast, so the object and its method table are package variables that never
// move and never free; reference counting is therefore constant.
type eventHandler struct{ vtbl *[4]uintptr }

var (
	handlerOnce   sync.Once
	handlerVtable [4]uintptr
	handler       eventHandler
	// handlerRoute receives the tag of a clicked toast.
	handlerRoute atomic.Pointer[func(tag string)]
)

func activatedHandler() *eventHandler {
	handlerOnce.Do(func() {
		handlerVtable = [4]uintptr{
			syscall.NewCallback(handlerQueryInterface),
			syscall.NewCallback(handlerAddRef),
			syscall.NewCallback(handlerRelease),
			syscall.NewCallback(handlerInvoke),
		}
		handler.vtbl = &handlerVtable
	})
	return &handler
}

func handlerQueryInterface(this *eventHandler, iid *windows.GUID, out **eventHandler) uintptr {
	if *iid == iidUnknown || *iid == iidAgileObject || *iid == iidToastActivatedHandler {
		*out = this
		return 0
	}
	*out = nil
	return eNoInterface
}

func handlerAddRef(*eventHandler) uintptr  { return 1 }
func handlerRelease(*eventHandler) uintptr { return 1 }

// handlerInvoke runs on a Windows thread-pool thread when the user clicks a
// toast. It reads the toast's tag and hands it on without blocking.
func handlerInvoke(_ *eventHandler, sender *comObject, _ *comObject) uintptr {
	route := handlerRoute.Load()
	if sender == nil || route == nil {
		return 0
	}
	toast, err := sender.query(&iidToastNotification2)
	if err != nil {
		return 0
	}
	defer toast.release()
	var tag hstring
	hr, _, _ := syscall.SyscallN(toast.vtbl[slotToast2GetTag], toast.self(), uintptr(unsafe.Pointer(&tag)))
	if check("get_Tag", hr) != nil {
		return 0
	}
	defer tag.free()
	(*route)(tag.String())
	return 0
}
