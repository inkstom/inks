// Package macmenu installs a native macOS menu bar.
//
// Items either carry a standard Cocoa action (hide:, terminate:, …) or an ID
// that is reported back through the fire callback. When an item is triggered
// by its key equivalent, the key event is forwarded to the window instead, so
// key handling (chords, user key bindings, text fields) stays in one place.
package macmenu

import "encoding/json"

// Modifier flags (NSEventModifierFlags).
const (
	Shift   = 1 << 17
	Control = 1 << 18
	Option  = 1 << 19
	Command = 1 << 20
)

// Item is a menu or menu item.
type Item struct {
	Title    string `json:"title,omitempty"`
	ID       int    `json:"id,omitempty"`
	Key      string `json:"key,omitempty"`
	Mods     int    `json:"mods,omitempty"`
	Selector string `json:"sel,omitempty"`
	Role     string `json:"role,omitempty"` // "services", "window" (submenus Cocoa manages)
	Sep      bool   `json:"sep,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	Sub      []Item `json:"sub,omitempty"`
}

var (
	fire       func(id int)
	shouldQuit func() bool
)

// Set replaces the menu bar. fire is called (on the Cocoa main thread) with
// the ID of a clicked item.
func Set(menus []Item, onFire func(id int)) {
	fire = onFire
	data, err := json.Marshal(menus)
	if err != nil {
		return
	}
	setMenu(data)
}

// OnTerminate registers fn to run (on the main thread) before the app quits
// from the menu, the Dock or logout. Returning false cancels quitting.
func OnTerminate(fn func() bool) {
	shouldQuit = fn
	installTerminate()
}
