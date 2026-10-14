//go:build !darwin

package macmenu

func setMenu([]byte)    {}
func installTerminate() {}

// Dump lists the installed menu bar (for tests).
func Dump() string { return "" }

func PlaceWindowAtMouse(uintptr)           {}
func Terminate()                           {}
func installReopen()                       {}
func Popup(items []string, done func(int)) { done(-1) }

func AcceptFirstMouse() {}
