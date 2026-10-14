//go:build !darwin

package macmenu

func setMenu([]byte)    {}
func installTerminate() {}

// Dump lists the installed menu bar (for tests).
func Dump() string { return "" }
