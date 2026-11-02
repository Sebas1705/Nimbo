//go:build windows

package main

import (
	"log"
	"runtime"

	"github.com/energye/systray"
)

// The tray icon. Wails v2 has none of its own. energye/systray is a maintained fork of getlantern/systray that
// adds click and double-click handlers, talks to the Windows shell directly (no cgo) and runs its own message
// loop on a dedicated thread, so it sits next to the Wails window without sharing its loop.
func startTray(show, quit func()) {
	go func() {
		// The shell delivers tray messages to the thread that created the icon: pin it.
		runtime.LockOSThread()
		systray.Run(func() {
			systray.SetIcon(trayIcon)
			systray.SetTitle("Nimbo")
			systray.SetTooltip("Nimbo " + shownVersion())
			// A click on the icon, like the Kotlin tray's double click, brings the window back.
			systray.SetOnClick(func(systray.IMenu) { show() })
			systray.SetOnDClick(func(systray.IMenu) { show() })
			systray.AddMenuItem("Open Nimbo", "Show the Nimbo window").Click(show)
			systray.AddSeparator()
			systray.AddMenuItem("Quit Nimbo", "Stop Nimbo and close the window").Click(quit)
			log.Printf("Tray icon added.")
		}, func() {})
	}()
}

// stopTray removes the icon so it does not linger in the tray after Nimbo exits.
func stopTray() { systray.Quit() }
