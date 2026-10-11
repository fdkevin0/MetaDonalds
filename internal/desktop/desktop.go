//go:build desktop

package desktop

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Run opens the desktop interface for an already-running service.
func Run(ctx context.Context, serverURL string, stopServer func()) error {
	assets, err := handler(serverURL)
	if err != nil {
		return err
	}
	app := application.New(application.Options{
		Name: "MetaDonalds",
		// Native termination can exit before the command's deferred cleanup runs.
		OnShutdown:                  stopServer,
		DisableDefaultSignalHandler: true,
		Description:                 "Search Nyaa and submit resources to PikPak",
		Assets:                      application.AssetOptions{Handler: assets},
		Mac:                         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "MetaDonalds", Width: 1120, Height: 820,
		MinWidth: 640, MinHeight: 540, URL: "/",
	})
	// Native window creation and Run stay on the main goroutine.
	stop := context.AfterFunc(ctx, app.Quit)
	defer stop()
	return app.Run()
}
