//go:build windows

package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// openExternalLinks sends links to other sites to the default browser. Without it the webview would open them in
// a blank in-app window. It is injected once the page has loaded, so the UI itself needs no change.
const openExternalLinks = `(function () {
  if (window.__nimboLinks) return;
  window.__nimboLinks = true;
  document.addEventListener('click', function (e) {
    var a = e.target && e.target.closest ? e.target.closest('a[href]') : null;
    if (!a) return;
    var u;
    try { u = new URL(a.href, location.href); } catch (_) { return; }
    if ((u.protocol === 'http:' || u.protocol === 'https:') && u.hostname !== 'wails.localhost') {
      e.preventDefault();
      window.runtime.BrowserOpenURL(u.href);
    }
  }, true);
})();`

// instanceID names the single-instance lock. A throwaway data folder (NIMBO_DATA_DIR) is a different instance on
// purpose, so a test copy never collides with the app the person really uses.
func instanceID(env map[string]string) string {
	id := "es.sebas1705.nimbo"
	if d := strings.TrimSpace(env["NIMBO_DATA_DIR"]); d != "" {
		sum := sha1.Sum([]byte(strings.ToLower(filepath.Clean(d))))
		id += "." + hex.EncodeToString(sum[:6])
	}
	return id
}

// runDesktop is the Windows app: a Wails window whose asset server serves the UI and, straight from memory, the API.
// No browser, and no TCP port is opened. Closing the window hides it to the tray; Quit in the tray exits.
func runDesktop(environ []string) int {
	env := envMap(environ)
	home, _ := os.UserHomeDir()
	dir := shellDataDir(env, home)

	// A windowed app has no console: whatever it prints goes to nimbo.log.
	if f, err := openLog(dir); err == nil {
		defer f.Close()
		os.Stdout, os.Stderr = f, f
		log.SetOutput(f)
		log.SetFlags(log.LstdFlags)
	}

	api, closer := buildAPI(env, home, "desktop", "127.0.0.1", 0)
	log.Printf("Nimbo %s starting (desktop window, in-process API)", shownVersion())

	var (
		ctx      context.Context
		quitting atomic.Bool
	)
	show := func() {
		if ctx == nil {
			return
		}
		wruntime.WindowUnminimise(ctx)
		wruntime.WindowShow(ctx)
		wruntime.Show(ctx)
	}
	quit := func() {
		quitting.Store(true)
		if ctx != nil {
			wruntime.Quit(ctx)
		}
	}

	err := wails.Run(&options.App{
		Title:     "Nimbo",
		Width:     1280,
		Height:    820,
		MinWidth:  960,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets:  uiFS(),
			Handler: desktopAPI(api),
		},
		BackgroundColour: &options.RGBA{R: 0x0b, G: 0x0e, B: 0x17, A: 0xff},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: instanceID(env),
			// A second launch brings the running window forward instead of starting another Nimbo.
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { show() },
		},
		Windows: &windows.Options{
			// Keep the webview's cache next to Nimbo's own data, not under a folder named after the exe in %APPDATA%.
			WebviewUserDataPath: filepath.Join(dir, "webview"),
		},
		OnStartup: func(c context.Context) {
			ctx = c
			startTray(show, quit)
		},
		OnDomReady: func(c context.Context) { wruntime.WindowExecJS(c, openExternalLinks) },
		OnBeforeClose: func(c context.Context) (prevent bool) {
			if quitting.Load() {
				return false
			}
			wruntime.WindowHide(c)
			return true
		},
		OnShutdown: func(context.Context) {
			stopTray()
			if closer != nil {
				_ = closer.Close()
			}
			log.Printf("Nimbo stopped")
		},
	})
	if err != nil {
		log.Printf("Nimbo could not start its window: %v", err)
		return 1
	}
	return 0
}
