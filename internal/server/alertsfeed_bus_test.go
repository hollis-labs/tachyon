package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Mail and seat rows share bus_events with the alert-worthy types, but they are activity: the Alerts feed
// never sees them, or every mail and every waiting seat would become an alert.
func TestAlertsBusSourceSkipsMailAndSeatRows(t *testing.T) {
	ctx := context.Background()
	db, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Writer.Submit(ctx, "test", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO bus_events(tether_seq,tether_event_id,at_ns,type,subject,severity) VALUES
	 (1,'e:1',1,'deploy','tachyon deployed','info'),(2,'e:2',2,'message','@a sent request to @b','info'),(3,'e:3',3,'seat','@a is waiting on @chrispian','warn'),(4,'e:4',4,'failure','disk alert raised','error')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	evs, err := busSource{db}.Events(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	if len(types) != 2 || types[0] != "deploy" || types[1] != "failure" {
		t.Fatalf("alerts read %v", types)
	}
}
