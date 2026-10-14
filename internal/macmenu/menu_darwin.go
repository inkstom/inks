package macmenu

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
void inks_set_menu(const char *json, int len);
void inks_install_terminate(void);
char *inks_dump_menu(void);
*/
import "C"

import "unsafe"

func setMenu(data []byte) {
	p := C.CBytes(data)
	C.inks_set_menu((*C.char)(p), C.int(len(data)))
	C.free(p)
}

func installTerminate() { C.inks_install_terminate() }

// Dump lists the installed menu bar (for tests). Must not be called on the
// main thread.
func Dump() string {
	p := C.inks_dump_menu()
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}

//export inksMenuFire
func inksMenuFire(id C.int) {
	if fire != nil {
		fire(int(id))
	}
}

//export inksShouldTerminate
func inksShouldTerminate() C.int {
	if shouldQuit == nil || shouldQuit() {
		return 1
	}
	return 0
}
