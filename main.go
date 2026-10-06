package main

import (
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	app.launchArgs = os.Args[1:]

	win := app.store.get().Window
	width, height := 1240, 800
	if win.Saved && win.Width >= 820 && win.Height >= 540 {
		width, height = win.Width, win.Height
	}
	state := options.Normal
	if win.Maximised {
		state = options.Maximised
	}

	// Development sessions (PIXELDRAIN_DESKTOP_HOME) get their own lock so
	// they can run next to the installed app instead of handing off to it.
	instanceID := "pixeldrain-desktop-2f6c1e0a"
	if os.Getenv("PIXELDRAIN_DESKTOP_HOME") != "" {
		instanceID += "-dev"
	}

	err := wails.Run(&options.App{
		Title:            "Pixeldrain",
		Width:            width,
		Height:           height,
		MinWidth:         820,
		MinHeight:        540,
		WindowStartState: state,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 26, G: 30, B: 38, A: 1},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		// Links and files passed to a second launch (e.g. "Send to") are
		// forwarded to the window that is already open.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               instanceID,
			OnSecondInstanceLaunch: app.secondInstance,
		},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		Windows: &windows.Options{
			Theme:           windows.SystemDefault,
			WindowClassName: windowClass,
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
