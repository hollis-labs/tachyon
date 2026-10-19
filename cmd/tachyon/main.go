// Command tachyon serves the Tachyon control plane: the embedded UI, the
// same-origin API and the first-party plugin processes, on app-host.
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hollis-labs/libs/app/app-host/host"

	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/agentclient"
	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/chatbridge"
	"github.com/hollis-labs/tachyon/internal/dataverbs"
	"github.com/hollis-labs/tachyon/internal/objectsync"
	"github.com/hollis-labs/tachyon/internal/planner"
	"github.com/hollis-labs/tachyon/internal/server"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
	"github.com/hollis-labs/tachyon/internal/webui"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 && (os.Args[1] == "access" || os.Args[1] == "data" || os.Args[1] == "local-data") {
		var err error
		if os.Args[1] == "access" {
			err = agentaccess.Command(ctx, env("TACHYON_DATA_DIR", xdg("XDG_DATA_HOME", ".local/share")), os.Args[2:], os.Stdout)
		} else if os.Args[1] == "local-data" {
			err = dataverbs.Command(ctx, env("TACHYON_DATA_DIR", xdg("XDG_DATA_HOME", ".local/share")), os.Args[2:], os.Stdin, os.Stdout)
		} else {
			err = agentclient.Command(ctx, os.Args[2:], os.Stdout)
		}
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	cfg := server.Config{
		ApprovalProducerConfigFile: os.Getenv("TACHYON_APPROVAL_PRODUCER_CONFIG"),
		CachePolicyFile:            os.Getenv("TACHYON_CACHE_POLICY_FILE"),
		ChatSyncInterval:           chatInterval(),
		ApprovalQueueURL:           env("TACHYON_APPROVAL_QUEUE_URL", "http://127.0.0.1:7843"),
		FragmentsURL:               env("FRAGMENTS_ENGINE_URL", "http://127.0.0.1:8091"),
		FragmentsTokenFile:         upstreamauth.TokenFile(nil, "", "FRAGMENTS_ENGINE_TOKEN_FILE"),
		TranscriptMaxSessions:      transcriptLimit(),
		PluginBinDir:               env("TACHYON_PLUGIN_DIR", "bin/plugins"),
		DataDir:                    env("TACHYON_DATA_DIR", xdg("XDG_DATA_HOME", ".local/share")),
		CacheDir:                   env("TACHYON_CACHE_DIR", xdg("XDG_CACHE_HOME", ".cache")),
		Upstreams: catalog.Upstreams{
			ActivityURL:       env("TACHYON_ACTIVITY_URL", "http://127.0.0.1:8099"),
			TetherAddr:        os.Getenv("TETHER_ADDR"),
			TetherTokenFile:   os.Getenv("TETHER_TOKEN_FILE"),
			CerberusAddr:      os.Getenv("CERBERUS_ADDR"),
			CerberusTokenFile: upstreamauth.TokenFile(nil, "", "CERBERUS_TOKEN_FILE"),
			TorqueURL:         env("TORQUE_URL", "http://127.0.0.1:8990"),
			TorqueToken:       os.Getenv("TORQUE_API_TOKEN"),
			GitHubToken:       githubToken(ctx),
			GitHubOrg:         env("GITHUB_ORG", "hollis-labs"),
		},
	}
	if len(os.Args) > 1 && os.Args[1] == "import-planner" {
		if err := planner.Command(ctx, cfg.DataDir, cfg.Upstreams, os.Args[2:], os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backfill-session-links" {
		if err := chatbridge.BackfillCommand(ctx, cfg.DataDir, cfg.Upstreams.TetherAddr, os.Args[2:], os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "sync-objects" {
		if err := objectsync.Command(ctx, cfg.DataDir, cfg.Upstreams, os.Args[2:], os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	srv := server.New(cfg)
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

func githubToken(ctx context.Context) string {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

func chatInterval() time.Duration {
	value := env("TACHYON_CHAT_SYNC_INTERVAL", "5s")
	interval, err := time.ParseDuration(value)
	if err != nil || interval < time.Second || interval > time.Hour {
		log.Fatal("TACHYON_CHAT_SYNC_INTERVAL must be between 1s and 1h")
	}
	return interval
}

func transcriptLimit() int {
	limit, err := strconv.Atoi(env("TACHYON_TRANSCRIPT_MAX_SESSIONS", "5000"))
	if err != nil || limit < 200 || limit > 20000 {
		log.Fatal("TACHYON_TRANSCRIPT_MAX_SESSIONS must be between 200 and 20000")
	}
	return limit
}
