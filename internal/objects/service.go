package objects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const stamp = "2006-01-02T15:04:05.000000000Z"

func timestamp(t time.Time) string { return t.UTC().Format(stamp) }
func now() time.Time               { return time.Now().UTC() }

type Service struct{ store *localstore.Store }

func New(store *localstore.Store) *Service { return &Service{store: store} }

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const columns = `o.id,o.kind,o.ownership,o.name,o.revision,o.local_json,o.metadata_json,o.deleted_at,o.created_at,o.updated_at,
 s.provider,s.instance,s.collection,s.external_id,s.version,s.observed_at,s.deleted_at,s.snapshot_json`
const joined = ` FROM objects o LEFT JOIN object_sources s ON s.object_id=o.id `

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Record, error) {
	var r Record
	var local, metadata, created, updated string
	var deleted, provider, instance, collection, external, version, observed, sourceDeleted, snapshot sql.NullString
	err := row.Scan(&r.ID, &r.Kind, &r.Ownership, &r.Name, &r.Revision, &local, &metadata, &deleted, &created, &updated, &provider, &instance, &collection, &external, &version, &observed, &sourceDeleted, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.Local = json.RawMessage(local)
	r.Metadata = json.RawMessage(metadata)
	r.CreatedAt, err = time.Parse(stamp, created)
	if err != nil {
		return r, err
	}
	r.UpdatedAt, err = time.Parse(stamp, updated)
	if err != nil {
		return r, err
	}
	if deleted.Valid {
		t, e := time.Parse(stamp, deleted.String)
		if e != nil {
			return r, e
		}
		r.DeletedAt = &t
	}
	base := json.RawMessage(`{}`)
	if provider.Valid {
		t, e := time.Parse(stamp, observed.String)
		if e != nil {
			return r, e
		}
		r.Source = &Source{Key: SourceKey{SourceScope: SourceScope{Provider: provider.String, Instance: instance.String, Collection: collection.String, Kind: r.Kind}, ExternalID: external.String}, Version: version.String, ObservedAt: t, Definition: json.RawMessage(snapshot.String)}
		if sourceDeleted.Valid {
			t, e := time.Parse(stamp, sourceDeleted.String)
			if e != nil {
				return r, e
			}
			r.Source.DeletedAt = &t
		}
		if r.Ownership == "source" {
			base = r.Source.Definition
		}
	}
	r.Definition, err = merge(base, r.Local, false)
	return r, err
}
func get(ctx context.Context, q queryer, id string) (Record, error) {
	return scan(q.QueryRowContext(ctx, "SELECT "+columns+joined+"WHERE o.id=?", id))
}
func (s *Service) Get(ctx context.Context, id string) (Record, error) {
	return get(ctx, s.store.Reader, id)
}
func lookup(ctx context.Context, q queryer, key SourceKey) (Record, error) {
	return scan(q.QueryRowContext(ctx, "SELECT "+columns+joined+"WHERE s.provider=? AND s.instance=? AND s.collection=? AND s.kind=? AND s.external_id=?", key.Provider, key.Instance, key.Collection, key.Kind, key.ExternalID))
}
func (s *Service) LookupSource(ctx context.Context, key SourceKey) (Record, error) {
	return lookup(ctx, s.store.Reader, key)
}
func pageLimit(limit int) (int, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("%w: limit must be 1–500", ErrInvalid)
	}
	return limit, nil
}
func (s *Service) List(ctx context.Context, f Filter) ([]Record, error) {
	if f.Kind != "" && !validKind(f.Kind) {
		return nil, ErrInvalid
	}
	limit, err := pageLimit(f.Limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Reader.QueryContext(ctx, "SELECT "+columns+joined+`WHERE (?='' OR o.kind=?) AND o.id>? AND (? OR (o.deleted_at IS NULL AND (o.ownership='tachyon' OR s.deleted_at IS NULL))) ORDER BY o.id LIMIT ?`, f.Kind, f.Kind, f.AfterID, f.IncludeDeleted, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) History(ctx context.Context, id string, after int64, limit int) ([]Revision, error) {
	limit, err := pageLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Reader.QueryContext(ctx, `SELECT record_json,operation,actor,recorded_at FROM object_revisions WHERE object_id=? AND revision>? ORDER BY revision LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var r Revision
		var body, at string
		if err := rows.Scan(&body, &r.Operation, &r.Actor, &at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &r.Record); err != nil {
			return nil, err
		}
		r.RecordedAt, err = time.Parse(stamp, at)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) Links(ctx context.Context, id string, incoming bool) ([]Link, error) {
	field := "from_id"
	if incoming {
		field = "to_id"
	}
	rows, err := s.store.Reader.QueryContext(ctx, "SELECT from_id,relation,slot,to_id FROM object_links WHERE "+field+"=? ORDER BY from_id,relation,slot,to_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.FromID, &l.Relation, &l.Slot, &l.ToID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func nullable(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timestamp(*t)
}

// persist changes the record, its typed edges and its immutable revision in the
// same queued transaction. The queue savepoint rolls everything back on error.
func persist(ctx context.Context, tx *sql.Tx, r *Record, operation, actor string) error {
	if actor == "" {
		return fmt.Errorf("%w: actor required", ErrInvalid)
	}
	base := json.RawMessage(`{}`)
	if r.Source != nil && r.Ownership == "source" {
		base = r.Source.Definition
	}
	if _, err := jsonObject(r.Metadata); err != nil {
		return err
	}
	if _, err := jsonObject(r.Local); err != nil {
		return err
	}
	effective, err := merge(base, r.Local, false)
	if err != nil {
		return err
	}
	name, links, err := validateDefinition(r.Kind, effective)
	if err != nil {
		return err
	}
	if len(effective) > 1<<20 {
		return fmt.Errorf("%w: effective definition exceeds 1 MiB", ErrInvalid)
	}
	r.Definition, r.Name = effective, name
	if err := validatePlanningGraph(ctx, tx, *r, links); err != nil {
		return err
	}
	for _, l := range links {
		var kind Kind
		if err := tx.QueryRowContext(ctx, "SELECT kind FROM objects WHERE id=?", l.ToID).Scan(&kind); err != nil {
			return fmt.Errorf("%w: referenced %s %q does not exist", ErrInvalid, l.kind, l.ToID)
		}
		if kind != l.kind {
			return fmt.Errorf("%w: reference kind mismatch", ErrInvalid)
		}
	}
	r.Revision++
	r.UpdatedAt = now()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = r.UpdatedAt
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO objects(id,kind,ownership,name,revision,local_json,metadata_json,deleted_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET ownership=excluded.ownership,name=excluded.name,revision=excluded.revision,local_json=excluded.local_json,metadata_json=excluded.metadata_json,deleted_at=excluded.deleted_at,updated_at=excluded.updated_at`, r.ID, r.Kind, r.Ownership, r.Name, r.Revision, string(r.Local), string(r.Metadata), nullable(r.DeletedAt), timestamp(r.CreatedAt), timestamp(r.UpdatedAt))
	if err != nil {
		return err
	}
	if err := projectPlanning(ctx, tx, *r); err != nil {
		return err
	}
	if r.Source != nil {
		src := r.Source
		k := src.Key
		_, err = tx.ExecContext(ctx, `INSERT INTO object_sources(object_id,provider,instance,collection,kind,external_id,version,observed_at,deleted_at,snapshot_json) VALUES(?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(object_id) DO UPDATE SET version=excluded.version,observed_at=excluded.observed_at,deleted_at=excluded.deleted_at,snapshot_json=excluded.snapshot_json`, r.ID, k.Provider, k.Instance, k.Collection, k.Kind, k.ExternalID, src.Version, timestamp(src.ObservedAt), nullable(src.DeletedAt), string(src.Definition))
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM object_links WHERE from_id=?", r.ID); err != nil {
		return err
	}
	for _, l := range links {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO object_links(from_id,relation,slot,to_id) VALUES(?,?,?,?)`, r.ID, l.Relation, l.Slot, l.ToID); err != nil {
			return err
		}
	}
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO object_revisions(object_id,revision,operation,actor,recorded_at,record_json) VALUES(?,?,?,?,?,?)`, r.ID, r.Revision, operation, actor, timestamp(r.UpdatedAt), string(body))
	return err
}
func (s *Service) Create(ctx context.Context, d Definition, metadata json.RawMessage, actor string) (Record, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Record{}, err
	}
	return s.CreateWithID(ctx, id.String(), d, metadata, actor)
}

// CreateWithID supports safely retrying a local creation after an uncertain
// outcome. The caller retains a UUID; reusing it with different content conflicts.
// Queue cancellation can stop waiting after a write was accepted, so transports
// should use this method with their persisted request ID rather than retry Create.
func (s *Service) CreateWithID(ctx context.Context, id string, d Definition, metadata json.RawMessage, actor string) (Record, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return Record{}, fmt.Errorf("%w: canonical UUID required", ErrInvalid)
	}
	if d == nil {
		return Record{}, ErrInvalid
	}
	body, err := json.Marshal(d)
	if err != nil {
		return Record{}, err
	}
	body, err = canonical(body)
	if err != nil {
		return Record{}, err
	}
	meta, err := canonical(metadata)
	if err != nil {
		return Record{}, err
	}
	initial := Record{ID: id, Kind: d.ObjectKind(), Ownership: "tachyon", Local: body, Metadata: meta}
	var r Record
	err = s.store.Writer.Submit(ctx, "object_create", func(ctx context.Context, tx *sql.Tx) error {
		if actor == "" {
			return ErrInvalid
		}
		existing, err := get(ctx, tx, id)
		if err == nil {
			if existing.Kind != initial.Kind || existing.Ownership != "tachyon" || existing.Deleted() || string(existing.Local) != string(body) || string(existing.Metadata) != string(meta) {
				return ErrConflict
			}
			r = existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		r = initial
		return persist(ctx, tx, &r, "create", actor)
	})
	if err != nil {
		return Record{}, err
	}
	return r, nil
}
func (s *Service) Patch(ctx context.Context, id string, expected int64, p Patch, actor string) (Record, error) {
	def, err := canonical(p.Definition)
	if err != nil {
		return Record{}, err
	}
	meta, err := canonical(p.Metadata)
	if err != nil {
		return Record{}, err
	}
	var out Record
	err = s.store.Writer.Submit(ctx, "object_patch", func(ctx context.Context, tx *sql.Tx) error {
		r, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		if expected != r.Revision {
			return ErrConflict
		}
		local, err := merge(r.Local, def, true)
		if err != nil {
			return err
		}
		metadata, err := merge(r.Metadata, meta, true)
		if err != nil {
			return err
		}
		if actor == "" {
			return ErrInvalid
		}
		if string(local) == string(r.Local) && string(metadata) == string(r.Metadata) {
			out = r
			return nil
		}
		r.Local, r.Metadata = local, metadata
		if err = persist(ctx, tx, &r, "patch", actor); err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return out, nil
}
func (s *Service) SetDeleted(ctx context.Context, id string, expected int64, deleted bool, actor string) (Record, error) {
	var out Record
	err := s.store.Writer.Submit(ctx, "object_delete", func(ctx context.Context, tx *sql.Tx) error {
		r, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.Revision != expected {
			return ErrConflict
		}
		if actor == "" {
			return ErrInvalid
		}
		if (r.DeletedAt != nil) == deleted {
			out = r
			return nil
		}
		operation := "restore"
		r.DeletedAt = nil
		if deleted {
			t := now()
			r.DeletedAt = &t
			operation = "delete"
		}
		if err = persist(ctx, tx, &r, operation, actor); err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return out, nil
}

// TakeOwnership explicitly promotes the effective definition to a Tachyon master.
// Source observations continue as provenance, but cannot change the definition or
// delete the master. This is a local data operation, never a provider write.
func (s *Service) TakeOwnership(ctx context.Context, id string, expected int64, actor string) (Record, error) {
	var out Record
	err := s.store.Writer.Submit(ctx, "object_adopt", func(ctx context.Context, tx *sql.Tx) error {
		r, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.Revision != expected {
			return ErrConflict
		}
		if actor == "" {
			return ErrInvalid
		}
		if r.Ownership == "tachyon" {
			out = r
			return nil
		}
		if r.Deleted() {
			return fmt.Errorf("%w: restore before taking ownership", ErrInvalid)
		}
		r.Local = r.Definition
		r.Ownership = "tachyon"
		if err = persist(ctx, tx, &r, "take_ownership", actor); err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return out, nil
}
