package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A minimal COM IDropTarget. OLE calls it on the UI thread while a drag is
// over the catch layer; the files arrive as CF_HDROP.

const (
	dropEffectNone = 0
	dropEffectCopy = 1
	cfHDrop        = 15
	tymedHGlobal   = 1
	eNoInterface   = 0x80004002
)

var (
	iidIUnknown    = windows.GUID{Data1: 0x00000000, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDropTarget = windows.GUID{Data1: 0x00000122, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
)

type dropHandler interface {
	dragEnter(paths []string, allowed uint32, x, y int32) uint32
	dragOver(x, y int32) uint32
	dragLeave()
	dropped(x, y int32) uint32
}

type dropTarget struct {
	vtbl    *[7]uintptr
	handler dropHandler
}

var dropVtbl = [7]uintptr{
	syscall.NewCallback(func(this *dropTarget, iid *windows.GUID, out *uintptr) uintptr {
		if *iid == iidIUnknown || *iid == iidIDropTarget {
			*out = uintptr(unsafe.Pointer(this))
			return 0
		}
		*out = 0
		return eNoInterface
	}),
	// The target lives for the whole process, so reference counting is a no-op.
	syscall.NewCallback(func(this *dropTarget) uintptr { return 1 }),
	syscall.NewCallback(func(this *dropTarget) uintptr { return 1 }),
	syscall.NewCallback(func(this *dropTarget, data *dataObject, keys uint32, pt uintptr, effect *uint32) uintptr {
		x, y := unpackPoint(pt)
		*effect = this.handler.dragEnter(dropPaths(data), *effect, x, y)
		return 0
	}),
	syscall.NewCallback(func(this *dropTarget, keys uint32, pt uintptr, effect *uint32) uintptr {
		x, y := unpackPoint(pt)
		if *effect&dropEffectCopy == 0 {
			*effect = dropEffectNone
			return 0
		}
		*effect = this.handler.dragOver(x, y)
		return 0
	}),
	syscall.NewCallback(func(this *dropTarget) uintptr {
		this.handler.dragLeave()
		return 0
	}),
	syscall.NewCallback(func(this *dropTarget, data *dataObject, keys uint32, pt uintptr, effect *uint32) uintptr {
		x, y := unpackPoint(pt)
		if *effect&dropEffectCopy == 0 {
			*effect = dropEffectNone
			return 0
		}
		// Only ever Copy: the source then leaves the original where it is.
		*effect = this.handler.dropped(x, y)
		return 0
	}),
}

// POINTL is passed by value, packed into one 64-bit register.
func unpackPoint(pt uintptr) (int32, int32) {
	return int32(uint32(pt)), int32(uint32(pt >> 32))
}

type formatEtc struct {
	format uint16
	ptd    uintptr
	aspect uint32
	index  int32
	tymed  uint32
}

type stgMedium struct {
	tymed   uint32
	hGlobal uintptr
	release uintptr
}

// dataObject is the head of a COM IDataObject; GetData is vtable slot 3.
type dataObject struct{ vtbl *[4]uintptr }

// dropPaths reads the file list out of an IDataObject.
func dropPaths(data *dataObject) []string {
	if data == nil {
		return nil
	}
	f := formatEtc{format: cfHDrop, aspect: 1, index: -1, tymed: tymedHGlobal}
	var m stgMedium
	if hr, _, _ := syscall.SyscallN(data.vtbl[3], uintptr(unsafe.Pointer(data)), uintptr(unsafe.Pointer(&f)), uintptr(unsafe.Pointer(&m))); hr != 0 {
		return nil
	}
	defer pReleaseStgMedium.Call(uintptr(unsafe.Pointer(&m)))
	n, _, _ := pDragQueryFileW.Call(m.hGlobal, 0xFFFFFFFF, 0, 0)
	if n == 0 || n > 100 {
		return nil
	}
	paths := make([]string, 0, n)
	for i := uintptr(0); i < n; i++ {
		size, _, _ := pDragQueryFileW.Call(m.hGlobal, i, 0, 0)
		buf := make([]uint16, size+1)
		pDragQueryFileW.Call(m.hGlobal, i, uintptr(unsafe.Pointer(&buf[0])), size+1)
		paths = append(paths, syscall.UTF16ToString(buf))
	}
	return paths
}
