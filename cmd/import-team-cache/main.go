// import-team-cache installs an explicitly requested, provenance-labeled preview.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/hollis-labs/tachyon/internal/apicache"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func main() {
	db := flag.String("database", "", "Tachyon SQLite database")
	input := flag.String("input", "", "JSON array of saved team definitions")
	source := flag.String("source", "", "Human-readable source label")
	flag.Parse()
	if *db == "" || *input == "" || *source == "" {
		log.Fatal("--database, --input and --source are required")
	}
	f, err := os.Open(*input)
	if err != nil {
		log.Fatal(err)
	}
	body, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	_ = f.Close()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	store, err := localstore.Open(ctx, *db)
	if err != nil {
		log.Fatal(err)
	}
	settings, err := apicache.LoadSettings(ctx, store, os.Getenv("TACHYON_CACHE_POLICY_FILE"))
	if err != nil {
		_ = store.Close()
		log.Fatal(err)
	}
	cache := apicache.New(store, settings, nil)
	count, err := cache.ImportTeams(ctx, *source, body)
	closeErr := store.Close()
	if err != nil {
		log.Fatal(err)
	}
	if closeErr != nil {
		log.Fatal(closeErr)
	}
	fmt.Printf("Imported %d saved team versions into the SQLite cache. Source: %s\n", count, *source)
}
