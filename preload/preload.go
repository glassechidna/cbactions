package main

// #include <stdlib.h>
import "C"
import (
	"github.com/glassechidna/cbactions"
	"github.com/rainycape/dl"
	"syscall"
	"unsafe"
)

func main() {}

//export open
func open(path *C.char, mode int32, perm uint) int {
	gos := cbactions.RewritePath(C.GoString(path))
	fd, _ := syscall.Open(gos, int(mode), uint32(perm))
	return fd
}

var libc_xstat64 func(ver int, s *C.char, buf *C.char) int
var libc_lxstat64 func(ver int, s *C.char, buf *C.char) int

func init() {
	libc, err := dl.Open("libc", 0)
	if err != nil {
		panic(err)
	}

	libc.Sym("__xstat64", &libc_xstat64)
	libc.Sym("__lxstat64", &libc_lxstat64)
}

//export __xstat64
func __xstat64(ver int, path *C.char, buf *C.char) int {
	cs := C.CString(cbactions.RewritePath(C.GoString(path)))
	ret := libc_xstat64(ver, cs, buf)
	C.free(unsafe.Pointer(cs))
	return ret
}

//export __lxstat64
func __lxstat64(ver int, path *C.char, buf *C.char) int {
	cs := C.CString(cbactions.RewritePath(C.GoString(path)))
	ret := libc_lxstat64(ver, cs, buf)
	C.free(unsafe.Pointer(cs))
	return ret
}
