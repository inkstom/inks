package macmenu

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include <stdint.h>
void inks_set_menu(const char *json, int len);
void inks_install_terminate(void);
char *inks_dump_menu(void);
void inks_place_window_at_mouse(uintptr_t view);
void inks_terminate(void);
void inks_install_reopen(void);
void inks_accept_first_mouse(void);
void inks_popup(const char *json, int len, int token);
*/
import "C"

import (
	"encoding/json"
	"sync"
	"unsafe"
)

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

// PlaceWindowAtMouse moves the window owning an NSView under the pointer.
func PlaceWindowAtMouse(view uintptr) { C.inks_place_window_at_mouse(C.uintptr_t(view)) }

// Terminate quits the app (through applicationShouldTerminate).
func Terminate() { C.inks_terminate() }

func installReopen() { C.inks_install_reopen() }

//export inksReopen
func inksReopen() {
	if reopen != nil {
		reopen()
	}
}

var (
	popupMu    sync.Mutex
	popupSeq   int
	popupFuncs = map[int]func(int){}
)

// Popup shows a context menu at the pointer. "" makes a separator. done
// gets the chosen index, or -1, on the main thread.
func Popup(items []string, done func(int)) {
	popupMu.Lock()
	popupSeq++
	tok := popupSeq
	popupFuncs[tok] = done
	popupMu.Unlock()
	data, _ := json.Marshal(items)
	p := C.CBytes(data)
	C.inks_popup((*C.char)(p), C.int(len(data)), C.int(tok))
	C.free(p)
}

//export inksPopupDone
func inksPopupDone(tok, picked C.int) {
	popupMu.Lock()
	f := popupFuncs[int(tok)]
	delete(popupFuncs, int(tok))
	popupMu.Unlock()
	if f != nil {
		f(int(picked))
	}
}

// AcceptFirstMouse makes clicks in inactive windows act immediately.
func AcceptFirstMouse() { C.inks_accept_first_mouse() }
