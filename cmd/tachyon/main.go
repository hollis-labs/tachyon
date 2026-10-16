// Command tachyon serves the Tachyon control plane: the embedded UI, the
// same-origin API and the first-party plugin processes, on app-host.
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hollis-labs/libs/app/app-host/host"

	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/server"
	"github.com/hollis-labs/tachyon/internal/webui"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(server.Config{
		PluginBinDir: env("TACHYON_PLUGIN_DIR", "bin/plugins"),
		DataDir:      env("TACHYON_DATA_DIR", xdg("XDG_DATA_HOME", ".local/share")),
		CacheDir:     env("TACHYON_CACHE_DIR", xdg("XDG_CACHE_HOME", ".cache")),
		Upstreams: catalog.Upstreams{
			TorqueURL:   env("TORQUE_URL", "http://127.0.0.1:8990"),
			TorqueToken: os.Getenv("TORQUE_API_TOKEN"),
		},
	})
	if err := srv.Start(ctx); err != nil {
		log.Fatal(err)
	}

	assets, err := webui.Assets()
	if err != nil {
		log.Fatal(err)
	}
	app, err := host.New(host.Config{
		Name:     "Tachyon",
		Assets:   assets,
		BasePath: webui.BasePath,
		Routes:   srv.Routes(),
		Stop:     srv.Stop,
	})
	if err != nil {
		log.Fatal(err)
	}

	addr := env("LISTEN_ADDR", env("TACHYON_ADDR", ":18093"))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Tachyon %s listening on http://%s", server.Version, listener.Addr())
	if err := app.Serve(ctx, listener); err != nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func xdg(key, homeRel string) string {
	base := os.Getenv(key)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, homeRel)
	}
	return filepath.Join(base, "tachyon")
}
