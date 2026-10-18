package planner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

type portfolioSnapshot struct {
	Instance   string                       `json:"instance"`
	ObservedAt time.Time                    `json:"observed_at"`
	SHA256     string                       `json:"sha256"`
	Databases  map[string][]json.RawMessage `json:"databases"`
}

// readPortfolio deliberately reads full unfiltered arrays (brief is an MCP-only
// flag; passing it to Torque HTTP would filter out all records).
func readPortfolio(ctx context.Context, u catalog.Upstreams) (map[string][]json.RawMessage, error) {
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	out := map[string][]json.RawMessage{}
	for _, db := range portfolioDatabases {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(u.TorqueURL, "/")+"/api/v1/planner/"+db, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if u.TorqueToken != "" {
			req.Header.Set("Authorization", "Bearer "+u.TorqueToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", db, err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
		resp.Body.Close()
		if err != nil || len(body) > 16<<20 {
			return nil, fmt.Errorf("unreadable/oversized %s", db)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("read %s: HTTP %d", db, resp.StatusCode)
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(body, &rows); err != nil || rows == nil {
			return nil, fmt.Errorf("invalid %s array", db)
		}
		// Canonicalize JSON objects while retaining array order and exact numbers.
		for i, raw := range rows {
			var v any
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if err := d.Decode(&v); err != nil {
				return nil, err
			}
			rows[i], err = json.Marshal(v)
			if err != nil {
				return nil, err
			}
		}
		out[db] = rows
	}
	return out, nil
}

func canonicalJSON(raw json.RawMessage) []byte {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

// Command snapshots the source before applying. A second complete read must
// agree, protecting against edits during capture. Apply is explicitly opt-in.
// Each record commits atomically; a failed run can be retried safely. Missing
// upstream items never delete a Tachyon master.
func Command(ctx context.Context, dataDir string, u catalog.Upstreams, args []string, out io.Writer) error {
	f := flag.NewFlagSet("tachyon import-planner", flag.ContinueOnError)
	f.SetOutput(out)
	dir := f.String("data-dir", dataDir, "directory containing tachyon.db")
	path := f.String("snapshot", "", "new private JSON snapshot path (required)")
	apply := f.Bool("apply", false, "apply to owned records after validation; otherwise preview")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *path == "" {
		return errors.New("usage: tachyon import-planner --snapshot NEWFILE [--apply]")
	}
	u.TorqueURL = strings.TrimRight(strings.TrimSpace(u.TorqueURL), "/")
	base, err := url.Parse(u.TorqueURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "http" && base.Scheme != "https") {
		return errors.New("invalid Torque base URL")
	}
	if err := upstreamauth.Guard(u.TorqueURL, u.TorqueToken); err != nil {
		return err
	}
	at := time.Now().UTC()
	rows, err := readPortfolio(ctx, u)
	if err != nil {
		return err
	}
	verify, err := readPortfolio(ctx, u)
	if err != nil {
		return err
	}
	first, _ := json.Marshal(rows)
	second, _ := json.Marshal(verify)
	if !bytes.Equal(first, second) {
		return errors.New("Torque Planner changed during capture; retry before applying")
	}
	hash := sha256.Sum256(first)
	snapshot := portfolioSnapshot{Instance: u.TorqueURL, ObservedAt: at, SHA256: hex.EncodeToString(hash[:]), Databases: rows}
	observations := []objects.Observation{}
	counts := map[string]int{}
	for _, db := range portfolioDatabases {
		seen := map[string]bool{}
		for i, row := range rows[db] {
			obs, err := PortfolioObservation(u.TorqueURL, db, row, i, at)
			if err != nil {
				return err
			}
			if seen[obs.Key.ExternalID] {
				return fmt.Errorf("duplicate %s/%s", db, obs.Key.ExternalID)
			}
			seen[obs.Key.ExternalID] = true
			observations = append(observations, obs)
		}
		counts[db] = len(rows[db])
	}
	file, err := os.OpenFile(*path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(file).Encode(snapshot)
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	s, err := localstore.Open(ctx, filepath.Join(*dir, "tachyon.db"))
	if err != nil {
		return err
	}
	defer s.Close()
	service := objects.New(s)
	var conflicts []string
	for _, obs := range observations {
		r, err := service.LookupSource(ctx, obs.Key)
		if errors.Is(err, objects.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		changed := !bytes.Equal(canonicalJSON(obs.Definition), canonicalJSON(r.Source.Definition))
		if r.Ownership != "tachyon" || changed && (r.Deleted() || !bytes.Equal(canonicalJSON(r.Local), canonicalJSON(r.Source.Definition))) {
			conflicts = append(conflicts, obs.Key.Collection+"/"+obs.Key.ExternalID+" ("+r.ID+")")
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("local edits conflict with source changes; nothing applied: %s", strings.Join(conflicts, ", "))
	}
	if *apply {
		for _, obs := range observations {
			if _, err := service.RefreshImported(ctx, obs, "operator:planner-migration"); err != nil {
				return fmt.Errorf("import %s/%s (prior records may have committed; retry is safe): %w", obs.Key.Collection, obs.Key.ExternalID, err)
			}
		}
	}
	return json.NewEncoder(out).Encode(map[string]any{"applied": *apply, "counts": counts, "total": len(observations), "snapshot": *path, "sha256": snapshot.SHA256, "observed_at": at})
}
