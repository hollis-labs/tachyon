package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

// Command is a trusted local-operator transport. OS database access is its
// authority boundary; it never grants bearer scopes or exposes a listener.
func Command(ctx context.Context, dataDir string, args []string, in io.Reader, out io.Writer) error {
	f := flag.NewFlagSet("tachyon local-data", flag.ContinueOnError)
	f.SetOutput(out)
	dir := f.String("data-dir", dataDir, "directory containing tachyon.db")
	input := f.String("input", "{}", "verb input JSON; - reads stdin")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return errors.New("usage: tachyon local-data [--input JSON|-] verbs|VERB")
	}
	raw := []byte(*input)
	if *input == "-" {
		var err error
		raw, err = io.ReadAll(io.LimitReader(in, MaxInputBytes+1))
		if err != nil {
			return err
		}
	}
	if len(raw) > MaxInputBytes {
		return errors.New("input exceeds 3 MiB")
	}
	s, err := localstore.Open(ctx, filepath.Join(*dir, "tachyon.db"))
	if err != nil {
		return err
	}
	defer s.Close()
	p := NewLocal(objects.New(s))
	enc := json.NewEncoder(out)
	if f.Arg(0) == "verbs" {
		return enc.Encode(p.Verbs())
	}
	body, err := p.Invoke(ctx, f.Arg(0), raw, "operator:local:"+os.Getenv("USER"))
	if err != nil {
		return err
	}
	return enc.Encode(body)
}
