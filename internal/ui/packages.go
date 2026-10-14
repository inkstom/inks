package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"inks/internal/pkgctl"
	"inks/internal/platform"
	"inks/internal/settings"
)

// Package commands: install, remove, upgrade and list packages from the
// public package index.

func (a *App) packages() *pkgctl.Manager {
	if a.pkg == nil {
		a.pkg = pkgctl.New(packagesDir(), filepath.Join(settings.DataDir(), "Cache"), filepath.Join(settings.UserDir(), "Packages.inks-settings"))
	}
	return a.pkg
}

func (a *App) registerPackageCommands(reg func(name, caption string, fn func(map[string]any))) {
	reg("install_package", "Packages: Install Package", func(map[string]any) { a.showInstallPackage() })
	reg("remove_package", "Packages: Remove Package", func(map[string]any) {
		names := a.packages().Installed()
		if len(names) == 0 {
			a.setStatus("Packages: no packages to remove", 0)
			return
		}
		var items []QuickItem
		for _, n := range names {
			items = append(items, QuickItem{Caption: n, Detail: tildify(filepath.Join(packagesDir(), n)), Data: n})
		}
		a.pick(items, func(it *QuickItem) {
			name := it.Data.(string)
			if err := a.packages().Remove(name, platform.Trash); err != nil {
				go platform.Message("Unable to remove "+name, err.Error())
				return
			}
			a.setStatus("Packages: "+name+" has been removed", 6*time.Second)
			a.packagesChanged()
		})
	})
	reg("list_packages", "Packages: List Packages", func(map[string]any) {
		names := a.packages().Installed()
		if len(names) == 0 {
			a.setStatus("Packages: no packages installed — Packages: Install Package to add some", 0)
			return
		}
		var items []QuickItem
		for _, n := range names {
			items = append(items, QuickItem{Caption: n, Detail: tildify(filepath.Join(packagesDir(), n)), Data: n})
		}
		a.pick(items, func(it *QuickItem) { go platform.Open(filepath.Join(packagesDir(), it.Data.(string))) })
	})
	reg("upgrade_all_packages", "Packages: Upgrade All Packages", func(map[string]any) {
		m := a.packages()
		a.setStatus("Packages: checking for upgrades…", 30*time.Second)
		go func() {
			idx, err := m.Load(true)
			if err != nil {
				a.Do(func() { a.setStatus("Packages: "+err.Error(), 0) })
				return
			}
			n := 0
			for _, name := range m.Recorded() {
				if idx.Packages[name] == nil {
					continue
				}
				if _, err := m.Install(name, func(s string) { a.Do(func() { a.consoleLog("Packages: "+s, false) }) }); err == nil {
					n++
				}
			}
			a.Do(func() {
				a.setStatus(fmt.Sprintf("Packages: upgraded %d packages", n), 6*time.Second)
				a.packagesChanged()
			})
		}()
	})
}

func (a *App) showInstallPackage() {
	m := a.packages()
	a.setStatus("Packages: loading the package index…", 30*time.Second)
	go func() {
		idx, err := m.Load(false)
		a.Do(func() {
			if err != nil {
				a.setStatus("", time.Millisecond)
				go platform.Message("Unable to download the package list", err.Error())
				return
			}
			a.setStatus("", time.Millisecond)
			installed := map[string]bool{}
			for _, n := range m.Installed() {
				installed[n] = true
			}
			var items []QuickItem
			for name, p := range idx.Packages {
				if installed[name] {
					continue
				}
				detail := p.Description
				if p.Release.Version != "" {
					detail += "  ·  v" + p.Release.Version
				}
				if p.Homepage != "" {
					detail += "  ·  " + strings.TrimPrefix(strings.TrimPrefix(p.Homepage, "https://"), "http://")
				}
				items = append(items, QuickItem{Caption: name, Detail: detail, Data: name})
			}
			sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Caption) < strings.ToLower(items[j].Caption) })
			a.pick(items, func(it *QuickItem) { a.installPackage(it.Data.(string)) })
		})
	}()
}

func (a *App) installPackage(name string) {
	m := a.packages()
	a.setStatus("Packages: installing "+name+"…", 60*time.Second)
	go func() {
		ver, err := m.Install(name, func(s string) { a.Do(func() { a.consoleLog("Packages: "+s, false) }) })
		a.Do(func() {
			if err != nil {
				a.setStatus("", time.Millisecond)
				go platform.Message("Unable to install "+name, err.Error())
				return
			}
			a.setStatus("Packages: "+name+" "+ver+" has been installed", 6*time.Second)
			a.consoleLog("Packages: installed "+name+" "+ver, false)
			a.packagesChanged()
		})
	}()
}

// packagesChanged reloads package resources and restarts the plugin host.
func (a *App) packagesChanged() {
	a.loadPackageResources()
	a.py.stop()
	if hasPlugins() {
		a.py.start()
	}
}
