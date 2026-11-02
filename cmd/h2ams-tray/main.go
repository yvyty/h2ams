// Command h2ams-tray is a menu-bar / system-tray companion for h2ams: a dot
// that shows overall health, and a menu to start, stop, and switch the
// environments of every project h2ams has run.
//
// It is a separate binary because tray support needs cgo on macOS, while the
// main h2ams binary stays cgo-free and trivially cross-compilable.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/yvyty/h2ams/internal/notify"
	"github.com/yvyty/h2ams/internal/tray"
)

// version is set at link time like the main binary's.
var version = "dev"

const refreshEvery = 3 * time.Second

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("h2ams-tray version", version)
		return
	}
	h2ams, err := tray.FindH2ams()
	if err != nil {
		log.Fatal(err)
	}
	app := &trayApp{runner: tray.Runner{H2ams: h2ams}, refresh: make(chan struct{}, 1)}
	systray.Run(app.ready, func() {})
}

type trayApp struct {
	runner  tray.Runner
	refresh chan struct{}

	mu       sync.Mutex
	menuSig  string        // fingerprint of the menu currently shown
	menuDone chan struct{} // closed when the current menu is replaced
}

func (a *trayApp) ready() {
	systray.SetIcon(tray.Icon(tray.Idle, runtime.GOOS))
	systray.SetTooltip("h2ams")
	go a.loop()
}

// loop re-reads every project on a timer, or right after an action.
func (a *trayApp) loop() {
	tick := time.NewTicker(refreshEvery)
	defer tick.Stop()
	for {
		a.update()
		select {
		case <-tick.C:
		case <-a.refresh:
		}
	}
}

func (a *trayApp) requestRefresh() {
	select {
	case a.refresh <- struct{}{}:
	default:
	}
}

func (a *trayApp) update() {
	projects, err := tray.Load()
	if err != nil {
		log.Printf("loading projects: %v", err)
	}
	systray.SetIcon(tray.Icon(tray.Overall(projects), runtime.GOOS))
	systray.SetTooltip(tray.Summary(projects))

	menu := tray.Menu(projects)
	sig, _ := json.Marshal(menu)
	a.mu.Lock()
	defer a.mu.Unlock()
	if string(sig) == a.menuSig {
		return // rebuilding an open menu would close it under the user
	}
	a.menuSig = string(sig)
	if a.menuDone != nil {
		close(a.menuDone)
	}
	a.menuDone = make(chan struct{})
	systray.ResetMenu()
	for _, item := range menu {
		a.add(item, nil, a.menuDone)
	}
}

// add creates item (under parent, or at the top level when nil) and listens
// for clicks until done is closed.
func (a *trayApp) add(item tray.Item, parent *systray.MenuItem, done <-chan struct{}) {
	if item.Separator {
		if parent == nil {
			systray.AddSeparator()
		} else {
			parent.AddSeparator()
		}
		return
	}
	var mi *systray.MenuItem
	switch {
	case parent == nil && item.Checked:
		mi = systray.AddMenuItemCheckbox(item.Title, "", true)
	case parent == nil:
		mi = systray.AddMenuItem(item.Title, "")
	case item.Checked:
		mi = parent.AddSubMenuItemCheckbox(item.Title, "", true)
	default:
		mi = parent.AddSubMenuItem(item.Title, "")
	}
	if item.Disabled {
		mi.Disable()
	}
	for _, child := range item.Children {
		a.add(child, mi, done)
	}
	if item.Action == nil {
		return
	}
	action := *item.Action
	go func() {
		for {
			select {
			case <-mi.ClickedCh:
				a.handle(action)
			case <-done:
				return
			}
		}
	}()
}

func (a *trayApp) handle(action tray.Action) {
	switch action.Kind {
	case tray.ActQuit:
		systray.Quit()
		return
	case tray.ActRefresh:
		a.requestRefresh()
		return
	}
	go func() {
		if err := a.runner.Run(action); err != nil {
			log.Printf("%s: %v", action.Kind, err)
			_ = notify.Desktop().Notify("h2ams-tray", err.Error())
		}
		a.requestRefresh()
	}()
}
