package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite"
)

// LaunchStore holds launch intents and orchestration checkpoints, not session
// state. Provider-owned sessions remain in Nanite or Tether.
type LaunchStore struct{ db *sql.DB }

func OpenLaunchStore(path string) (*LaunchStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS launches (id TEXT PRIMARY KEY, body BLOB NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	return &LaunchStore{db: db}, nil
}
func (s *LaunchStore) Close() error { return s.db.Close() }
func (s *LaunchStore) Create(ctx context.Context, l *Launch) error {
	body, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO launches (id, body) VALUES (?, ?)", l.ID, body)
	return err
}
func decodeLaunch(body []byte) (*Launch, error) {
	var l Launch
	err := json.Unmarshal(body, &l)
	return &l, err
}
func (s *LaunchStore) Get(ctx context.Context, id string) (*Launch, error) {
	if id == "" {
		return nil, fmt.Errorf("launch_id is required")
	}
	var body []byte
	err := s.db.QueryRowContext(ctx, "SELECT body FROM launches WHERE id=?", id).Scan(&body)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("launch %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	return decodeLaunch(body)
}

// Update serializes checkpoint transitions in a transaction. Callbacks contain
// no network operations; failed commits cannot leave a cached state ahead of disk.
func (s *LaunchStore) Update(ctx context.Context, id string, fn func(*Launch) error) (*Launch, error) {
	if id == "" {
		return nil, fmt.Errorf("launch_id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var body []byte
	err = tx.QueryRowContext(ctx, "SELECT body FROM launches WHERE id=?", id).Scan(&body)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("launch %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	l, err := decodeLaunch(body)
	if err != nil {
		return nil, err
	}
	if err := fn(l); err != nil {
		return nil, err
	}
	body, err = json.Marshal(l)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE launches SET body=? WHERE id=?", body, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return l, nil
}
func (s *LaunchStore) List(ctx context.Context, req ListRequest) ([]Launch, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM launches")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Launch{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		l, err := decodeLaunch(body)
		if err != nil {
			return nil, err
		}
		if req.AgentID != "" && req.AgentID != l.AgentID || req.Provider != "" && req.Provider != l.Provider || req.State != "" && req.State != string(l.State) {
			continue
		}
		result = append(result, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if req.Limit > 0 && len(result) > req.Limit {
		result = result[:req.Limit]
	}
	return result, nil
}
