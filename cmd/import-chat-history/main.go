// import-chat-history copies Tangent's retained chat turns without modifying it.
// It is an explicit, repeatable migration, never a runtime dependency.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/chatbridge"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func main() {
	dbPath := flag.String("database", "", "Tachyon SQLite database")
	tangentURL := flag.String("tangent-url", "", "Tangent HTTP endpoint to import once")
	tetherAddr := flag.String("tether-address", "", "Tether source identity")
	tokenFile := flag.String("token-file", "", "Tether credential file for roster reads")
	syncNow := flag.Bool("sync", false, "Read Tether publications after import")
	flag.Parse()
	if *dbPath == "" || *tangentURL == "" || *tetherAddr == "" {
		log.Fatal("database, tangent-url and tether-address are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	source, err := chatbridge.NewSource(*tetherAddr, *tokenFile)
	if err != nil {
		log.Fatal(err)
	}
	agents, err := source.Agents(ctx)
	if err != nil {
		log.Fatal("could not read chat roster: ", err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := strings.TrimRight(*tangentURL, "/")
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/chat", nil)
	req.Header.Set("Accept", "text/html")
	res, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		log.Fatal("Tangent participant session unavailable: ", res.StatusCode)
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", base+"/api/turns", nil)
	req.Header.Set("Accept", "application/json")
	res, err = client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		log.Fatal("Tangent history unavailable: ", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20+1))
	if err != nil || len(raw) > 32<<20 {
		log.Fatal("history unreadable or oversized")
	}
	var in struct {
		Pending []json.RawMessage `json:"pending"`
		History []json.RawMessage `json:"history"`
	}
	if err = json.Unmarshal(raw, &in); err != nil {
		log.Fatal(err)
	}
	countReplies := 0
	keep := func(items []json.RawMessage) []json.RawMessage {
		out := []json.RawMessage{}
		for _, raw := range items {
			var t chatbridge.Turn
			if json.Unmarshal(raw, &t) != nil {
				log.Fatal("invalid turn")
			}
			for _, a := range agents {
				launch := ""
				if t.Source != nil {
					launch = t.Source.Attribution["launch_id"]
				}
				if launch == a.LaunchID || (launch == "" && t.AgentID == a.AgentID) {
					out = append(out, raw)
					if t.Resolution != nil {
						countReplies++
					}
					break
				}
			}
		}
		return out
	}
	in.Pending = keep(in.Pending)
	in.History = keep(in.History)
	raw, err = json.Marshal(in)
	if err != nil {
		log.Fatal(err)
	}
	db, err := localstore.Open(ctx, *dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	owner := &chatbridge.Owned{Store: db, SourceID: *tetherAddr}
	n, err := owner.Import(ctx, raw)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Imported %d chat turns including %d operator replies; Tangent unchanged.\n", n, countReplies)
	if *syncNow {
		owner.Source = source
		if err = owner.Sync(ctx); err != nil {
			log.Fatal("import saved, but direct Tether sync failed: ", err)
		}
		inbox, e := owner.Inbox(ctx)
		if e != nil {
			log.Fatal(e)
		}
		fmt.Printf("Direct Tether sync complete: %d total local turns, owner=%s.\n", len(inbox.Pending)+len(inbox.History), inbox.Owner)
	}

}
