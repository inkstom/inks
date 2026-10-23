//go:build !darwin

package macmenu

func setMenu([]byte)    {}
func installTerminate() {}

// Dump lists the installed menu bar (for tests).
func Dump() string { return "" }

func PlaceWindowAtMouse(uintptr) {}
func Terminate()                 {}
func installReopen()             {}

// PopupItem is one entry of a context menu.
type PopupItem struct {
	Title    string
	Key      string
	Mods     int
	Disabled bool
}

func Popup(items []PopupItem, done func(int)) { done(-1) }

func AcceptFirstMouse() {}

func DisableWindowTabbing() {}
func SetupWindow(uintptr)   {}
