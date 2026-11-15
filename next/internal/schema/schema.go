// Package schema describes a SQLite schema in enough detail to compare two databases exactly:
// every sqlite_master entry (type, table, stored SQL text) and, per table, pragma table_info,
// foreign_key_list, index_list and index_xinfo. The schema test (internal/store) and the data
// migration (internal/migratefrom) use the same description, so "equal" means the same thing to both.
package schema

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Querier is the read side both a *sql.DB and the database module's Reader() offer.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Object is one sqlite_master entry, with the table pragmas for a table.
type Object struct {
	Type, Table, SQL string
	Columns          []string // pragma table_info rows
	ForeignKeys      []string // pragma foreign_key_list rows
	Indexes          []string // pragma index_list rows, each with its index_xinfo
}

// Schema maps object names to their description.
type Schema map[string]Object

// Describe reads the schema of the main database behind q. skip, when not nil, leaves out every
// object whose table it names.
func Describe(ctx context.Context, q Querier, skip func(table string) bool) (Schema, error) {
	rows, err := q.QueryContext(ctx, `SELECT type, name, tbl_name, coalesce(sql, '') FROM main.sqlite_master`)
	if err != nil {
		return nil, err
	}
	out := Schema{}
	for rows.Next() {
		var o Object
		var name string
		if e := rows.Scan(&o.Type, &name, &o.Table, &o.SQL); e != nil {
			_ = rows.Close()
			return nil, e
		}
		if skip != nil && skip(o.Table) {
			continue
		}
		out[name] = o
	}
	if e := rows.Close(); e != nil {
		return nil, e
	}
	for name, o := range out {
		if o.Type != "table" {
			continue
		}
		if o.Columns, err = Pragma(ctx, q, "table_info", name); err != nil {
			return nil, err
		}
		if o.ForeignKeys, err = Pragma(ctx, q, "foreign_key_list", name); err != nil {
			return nil, err
		}
		ixs, err := Pragma(ctx, q, "index_list", name)
		if err != nil {
			return nil, err
		}
		for _, ix := range ixs {
			info, err := Pragma(ctx, q, "index_xinfo", strings.Split(ix, "|")[1])
			if err != nil {
				return nil, err
			}
			o.Indexes = append(o.Indexes, ix+" => "+strings.Join(info, "; "))
		}
		sort.Strings(o.Indexes)
		out[name] = o
	}
	return out, nil
}

var pragmas = []string{"table_info", "foreign_key_list", "index_list", "index_xinfo"}

// Pragma returns the rows of one of the table-valued pragmas above as "|"-joined strings.
func Pragma(ctx context.Context, q Querier, name, arg string) ([]string, error) {
	if !slices.Contains(pragmas, name) {
		return nil, fmt.Errorf("schema: pragma %q is not one this package reads", name)
	}
	rows, err := q.QueryContext(ctx, `SELECT * FROM pragma_`+name+`(?)`, arg) //nolint:gosec // name is checked against the fixed list above
	if err != nil {
		return nil, err
	}
	cols, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			_ = rows.Close()
			return nil, err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out, rows.Close()
}

// Diff lists every difference between want and got, sorted: objects missing or extra, and per object
// the type, table, SQL text, columns, foreign keys and indexes.
func Diff(want, got Schema) []string {
	var out []string
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			out = append(out, fmt.Sprintf("%s %s: missing", w.Type, name))
			continue
		}
		if w.Type != g.Type || w.Table != g.Table {
			out = append(out, fmt.Sprintf("%s: type/table %s/%s, want %s/%s", name, g.Type, g.Table, w.Type, w.Table))
		}
		if w.SQL != g.SQL {
			out = append(out, fmt.Sprintf("%s %s: sql differs\n  want: %s\n  got:  %s", w.Type, name, w.SQL, g.SQL))
		}
		for _, part := range []struct {
			what string
			w, g []string
		}{{"columns", w.Columns, g.Columns}, {"foreign keys", w.ForeignKeys, g.ForeignKeys}, {"indexes", w.Indexes, g.Indexes}} {
			if !slices.Equal(part.w, part.g) {
				out = append(out, fmt.Sprintf("table %s: %s differ\n  want: %q\n  got:  %q", name, part.what, part.w, part.g))
			}
		}
	}
	for name, g := range got {
		if _, ok := want[name]; !ok {
			out = append(out, fmt.Sprintf("%s %s: not in the reference", g.Type, name))
		}
	}
	sort.Strings(out)
	return out
}

// Tables returns the table names of s, sorted.
func (s Schema) Tables() []string {
	var out []string
	for name, o := range s {
		if o.Type == "table" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
