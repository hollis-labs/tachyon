package chatbridge

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/tachyon/internal/apicache"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// BackfillCommand is offline maintenance over retained history. It neither
// fetches upstream nor dispatches replies or starts the host/scheduler.
func BackfillCommand(ctx context.Context, dataDir, source string, args []string, out io.Writer) error {
	f := flag.NewFlagSet("backfill-session-links", flag.ContinueOnError)
	f.SetOutput(out)
	dir := f.String("data-dir", dataDir, "directory containing tachyon.db")
	addr := f.String("tether-address", source, "original Tether source identity stored with chat history")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || strings.TrimSpace(*addr) == "" || *dir == "" {
		return errors.New("data directory and original Tether address required")
	}
	db, err := localstore.Open(ctx, filepath.Join(*dir, "tachyon.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	o := Owned{Store: db, SourceID: *addr}
	n, err := o.BackfillSessionLinks(ctx, apicache.Digest("chat-owner\n"+*addr))
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"linked": n})
}
