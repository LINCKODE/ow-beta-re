// Storage explorer: a desktop app over the offline content reader (systems/content) and the STU reader
// (systems/stu). Wails v2 (Go backend, native window) + a Nuxt 3 SPA (frontend/, built to frontend/dist).
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:            "OW Storage Explorer",
		Width:            1480,
		Height:           920,
		MinWidth:         960,
		MinHeight:        600,
		AssetServer:      &assetserver.Options{Assets: assets, Handler: imageHandler{app}}, // texture images (app_texture.go)
		BackgroundColour: &options.RGBA{R: 14, G: 19, B: 30, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
