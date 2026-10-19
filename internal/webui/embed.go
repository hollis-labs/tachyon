// Package webui embeds the built frontend served by app-host.
package webui

import (
	"embed"
	"io/fs"
)

// BasePath is where app-host mounts the UI.
const BasePath = "/"

//go:embed all:dist
var embedded embed.FS

// Assets returns the built frontend. dist/.gitkeep keeps the embed compiling
// before the first `make ui-build`.
func Assets() (fs.FS, error) { return fs.Sub(embedded, "dist") }
