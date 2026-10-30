package chatbridge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// Owned is Tachyon's chat history owner. Source publications and reply intent
// survive response-cache eviction. No runtime request goes through Tangent.
type Owned struct {
	Store             *localstore.Store
	Source            Source
	SourceID          string
	ConfigError       error
	Interval          time.Duration
	FreshSeconds      int
	RevalidateSeconds int
	CacheEnabled      bool
	// Changed is called after a Tether turn is stored ("message") or its reply delivery moves ("delivery").
	// It must not block; it may be nil.
	Changed func(ctx context.Context, kind string, t Turn)
	mu                sync.Mutex
	cancel            context.CancelFunc
	done              chan struct{}
}
type Resolution struct {
	ID   string `json:"resolution_id"`
	Text string `json:"response_text"`
	At   string `json:"resolved_at"`
}
type SourceMessage struct {
	Origin      string            `json:"origin"`
	Endpoint    string            `json:"endpoint_ref"`
	Channel     string            `json:"channel"`
	MessageID   string            `json:"message_id"`
	Sequence    int64             `json:"sequence"`
	Sender      string            `json:"sender_urn"`
	OutputID    string            `json:"output_id,omitempty"`
	Attribution map[string]string `json:"attribution,omitempty"`
}
type Turn struct {
	ID             string          `json:"item_id"`
	AgentID        string          `json:"agent_id"`
	SessionID      string          `json:"session_id,omitempty"`
	LocalSessionID string          `json:"local_session_id,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	Kind           string          `json:"kind"`
	Content        string          `json:"content"`
	Created        string          `json:"created_at"`
	Revision       int64           `json:"revision"`
	Sequence       int64           `json:"queue_sequence"`
	State          string          `json:"state"`
	Delivery       string          `json:"delivery_state"`
	Replyable      bool            `json:"replyable"`
	Resolution     *Resolution     `json:"resolution,omitempty"`
	Source         *SourceMessage  `json:"source_message,omitempty"`
	Trace          json.RawMessage `json:"stage_trace,omitempty"`
}
type Inbox struct {
	Pending  []Turn `json:"pending"`
	History  []Turn `json:"history"`
	SyncedAt string `json:"synced_at"`
	Problem  string `json:"sync_problem,omitempty"`
	Owner    string `json:"owner"`
}

func (o *Owned) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	o.cancel = cancel
	o.done = make(chan struct{})
	go func() {
		defer close(o.done)
		interval := o.Interval
		if interval <= 0 {
			interval = 5 * time.Second
		}
		for {
			if ctx.Err() != nil {
				return
			}
			c, finish := context.WithTimeout(ctx, 40*time.Second)
			_ = o.Sync(c)
			finish()
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
}
func (o *Owned) Stop() {
	if o.cancel != nil {
		o.cancel()
		<-o.done
	}
}
func digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func (o *Owned) saveTurn(ctx context.Context, tx *sql.Tx, t Turn, imported bool) error {
	t.LocalSessionID = "" // derived separately, never trusted from imported JSON
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	channel, pub, launch := "", "", ""
	if t.Source != nil {
		channel = t.Source.Channel
		pub = t.Source.MessageID
		launch = t.Source.Attribution["launch_id"]
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO chat_turns(id,source,channel,publication_id,launch_id,session_id,created_at,revision,body,imported)
 VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, t.ID, o.SourceID, channel, pub, launch, t.SessionID, t.Created, t.Revision, raw, imported)
	if err != nil {
		return err
	}
	return linkTurnSession(ctx, tx, o.SourceID, t.ID)
}
func (o *Owned) get(ctx context.Context, id string) (Turn, error) {
	var t Turn
	var raw []byte
	err := o.Store.Reader.QueryRowContext(ctx, "SELECT body FROM chat_turns WHERE source=? AND id=?", o.SourceID, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &t)
	}
	return t, err
}
func (o *Owned) updateTurn(ctx context.Context, tx *sql.Tx, t Turn) error {
	t.LocalSessionID = ""
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE chat_turns SET revision=?,body=? WHERE source=? AND id=?`, t.Revision, raw, o.SourceID, t.ID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO chat_turn_revisions(turn_id,revision,body,recorded_at) VALUES(?,?,?,?)`, t.ID, t.Revision, raw, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (o *Owned) Sync(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	err := o.sync(ctx)
	status := problem(err)
	// Persist the observation even if the request that initiated sync went away.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	storeErr := o.Store.Writer.Submit(persist, "chat_sync_status", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO chat_sync_status(source,updated_at,problem) VALUES(?,?,?) ON CONFLICT(source) DO UPDATE SET updated_at=CASE WHEN excluded.problem='' THEN excluded.updated_at ELSE chat_sync_status.updated_at END,problem=excluded.problem`, o.SourceID, time.Now().UTC().Format(time.RFC3339Nano), status)
		return e
	})
	return errors.Join(err, storeErr)
}
func (o *Owned) sync(ctx context.Context) error {
	if o.ConfigError != nil {
		return fmt.Errorf("configuration: %w", o.ConfigError)
	}
	if o.Source == nil {
		return errors.New("configuration: Tether source missing")
	}
	agents, err := o.Source.Agents(ctx)
	if err != nil {
		return err
	}
	channels := map[string]bool{}
	for _, a := range agents {
		if a.Channel != "" {
			channels[a.Channel] = true
		}
	}
	for channel := range channels {
		var cursor int64
		err = o.Store.Reader.QueryRowContext(ctx, "SELECT cursor FROM chat_cursors WHERE source=? AND channel=?", o.SourceID, channel).Scan(&cursor)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		for page := 0; page < 100; page++ {
			batch, e := o.Source.ChannelMessages(ctx, channel, tether.ChannelMessagesOptions{Since: cursor, Limit: 100})
			if e != nil {
				return e
			}
			if batch.Name != channel || batch.NextSince < cursor {
				return errors.New("invalid_response: channel cursor")
			}
			if len(batch.Messages) == 0 {
				break
			}
			if batch.NextSince <= cursor {
				return errors.New("invalid_response: channel did not advance")
			}
			var turns []Turn
			previous := cursor
			for _, m := range batch.Messages {
				if m.Seq <= previous || m.Seq > batch.NextSince {
					return errors.New("invalid_response: publication sequence")
				}
				previous = m.Seq
				t, e := publication(o.SourceID, channel, m)
				if e != nil {
					return e
				}
				if t != nil {
					turns = append(turns, *t)
				}
			}
			e = o.Store.Writer.Submit(ctx, "chat_ingest", func(ctx context.Context, tx *sql.Tx) error {
				for _, m := range batch.Messages {
					raw, e := json.Marshal(m)
					if e != nil {
						return e
					}
					if _, e = tx.ExecContext(ctx, "INSERT INTO chat_publications(source,channel,publication_id,sequence,envelope) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING", o.SourceID, channel, m.ID, m.Seq, raw); e != nil {
						return e
					}
				}
				for _, t := range turns {
					if e := o.saveTurn(ctx, tx, t, false); e != nil {
						return e
					}
				}
				_, e := tx.ExecContext(ctx, `INSERT INTO chat_cursors(source,channel,cursor) VALUES(?,?,?) ON CONFLICT(source,channel) DO UPDATE SET cursor=excluded.cursor`, o.SourceID, channel, batch.NextSince)
				return e
			})
			if e != nil {
				return e
			}
			if o.Changed != nil {
				for _, t := range turns {
					o.Changed(ctx, "message", t)
				}
			}
			cursor = batch.NextSince
			if len(batch.Messages) < 100 {
				break
			}
			if page == 99 {
				return errors.New("backfill_pending: more history remains")
			}
		}
	}
	return o.pollDeliveries(ctx)
}
func publication(sourceID, channel string, m tether.ChannelMessage) (*Turn, error) {
	if m.ID == "" || string(m.Channel) != channel || m.To.URN() != "msg://service/local/channel/"+channel {
		return nil, errors.New("invalid_response: publication identity")
	}
	if m.Purged {
		return nil, nil
	}
	var content string
	if err := json.Unmarshal(m.Payload, &content); err != nil {
		var b struct {
			Text *string `json:"text"`
			Body *string `json:"body"`
		}
		if json.Unmarshal(m.Payload, &b) != nil || (b.Text == nil) == (b.Body == nil) {
			return nil, errors.New("invalid_response: publication text")
		}
		if b.Text != nil {
			content = *b.Text
		} else {
			content = *b.Body
		}
	}
	md := m.Metadata
	kind := md["kind"]
	origin := "routed"
	replyable := true
	switch kind {
	case "final":
		kind = "terminal"
	case "question", "failure", "approval":
	default:
		kind = "checkpoint"
		origin = "publication"
		replyable = false
	}
	session, turn := md["session_id"], md["turn_id"]
	if origin == "routed" && (session == "" || turn == "" || m.From.URN() != "msg://session/local/"+session || m.ThreadID != session) {
		return nil, errors.New("invalid_response: routed identity")
	}
	t := &Turn{ID: "tether_" + digest(sourceID+"\n"+channel+"\n"+m.ID), AgentID: md["logical_agent_id"], SessionID: session, TurnID: turn, Kind: kind, Content: content, Created: m.CreatedAt.UTC().Format(time.RFC3339Nano), Revision: 1, Sequence: m.Seq, State: "presented", Delivery: "queued", Replyable: replyable, Source: &SourceMessage{Origin: origin, Endpoint: sourceID, Channel: channel, MessageID: m.ID, Sequence: m.Seq, Sender: m.From.URN(), OutputID: md["output_id"], Attribution: md}}
	if t.AgentID == "" {
		t.AgentID = m.From.URN()
	}
	return t, nil
}
func problem(err error) string {
	if err == nil {
		return ""
	}
	var api *tether.APIError
	if errors.As(err, &api) {
		if api.StatusCode == 401 || api.StatusCode == 403 {
			return "authentication"
		}
		return "remote"
	}
	if errors.Is(err, upstreamauth.ErrCredential) || strings.Contains(err.Error(), "configuration:") {
		return "configuration"
	}
	if strings.Contains(err.Error(), "invalid_response:") {
		return "invalid_response"
	}
	return "unreachable"
}
func (o *Owned) Inbox(ctx context.Context) (Inbox, error) {
	out := Inbox{Pending: []Turn{}, History: []Turn{}, Owner: "tachyon"}
	rows, err := o.Store.Reader.QueryContext(ctx, "SELECT t.body,COALESCE(l.session_object_id,'') FROM chat_turns t LEFT JOIN chat_session_links l ON l.turn_id=t.id WHERE t.source=? ORDER BY t.created_at,t.id", o.SourceID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		t, err := scanLinkedTurn(rows)
		if err != nil {
			return out, err
		}
		if t.Resolution == nil && t.State == "presented" {
			out.Pending = append(out.Pending, t)
		} else {
			out.History = append(out.History, t)
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	err = o.Store.Reader.QueryRowContext(ctx, "SELECT updated_at,problem FROM chat_sync_status WHERE source=?", o.SourceID).Scan(&out.SyncedAt, &out.Problem)
	if err == sql.ErrNoRows {
		out.Problem = "connecting"
		err = nil
	}
	return out, err
}
