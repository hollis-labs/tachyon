package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	libstore "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

// The Postgres migration set describes the same schema as the SQLite set (port plan section 14):
// the same versions, and per table the same columns in order (name, normalized type, nullability),
// primary key, unique column sets, named indexes (columns, uniqueness, partial or not), foreign keys
// and triggers. CHECK constraints and defaults are written per dialect and are not compared.
func TestPostgresSetMatchesSQLiteSet(t *testing.T) {
	storetest.RequirePostgres(t)
	if err := migrate.CheckParity(store.Source()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	lite, err := store.Open(ctx, libstore.Config{Driver: libstore.DriverSQLite, SQLite: libstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "tachyon.db")}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lite.Close() }()
	pg := storetest.New(t).Open(t)
	want := sqliteShape(t, lite.Lib())
	got := postgresShape(t, pg.Lib())
	if diff := shapeDiff(want, got); len(diff) > 0 {
		t.Fatalf("the Postgres set differs from the SQLite set in %d place(s):\n%s", len(diff), strings.Join(diff, "\n"))
	}
	// The comparison sees a missing column: drop one from the Postgres schema and compare again.
	if _, err := pg.Lib().ExecContext(ctx, `ALTER TABLE planner_events DROP COLUMN weight`); err != nil {
		t.Fatal(err)
	}
	if diff := shapeDiff(want, postgresShape(t, pg.Lib())); !slices.ContainsFunc(diff, func(d string) bool { return strings.Contains(d, "planner_events") }) {
		t.Fatalf("a dropped column was not reported: %v", diff)
	}
}

// storedAs are columns whose SQLite declared type is not what they hold: chat_events.body is
// declared TEXT but holds bytes, and is bytea on Postgres.
var storedAs = map[string]string{"chat_events.body": "blob"}

// postgresOnly are the parts of the Postgres set that stand in for something SQLite has implicitly:
// object_revisions.row_seq is SQLite's implicit row id (store.RevisionSeq).
var postgresOnly = map[string][]string{
	"object_revisions": {"column row_seq integer not null", "unique (row_seq)"},
}

// shape is one table: its normalized facts, one line each.
type shape map[string][]string

func shapeDiff(want, got shape) []string {
	var out []string
	for _, table := range union(keys(want), keys(got)) {
		w, inW := want[table]
		g, inG := got[table]
		switch {
		case !inW:
			out = append(out, "table "+table+": only on Postgres")
			continue
		case !inG:
			out = append(out, "table "+table+": missing on Postgres")
			continue
		}
		var extra []string
		for _, line := range g {
			if !slices.Contains(postgresOnly[table], line) {
				extra = append(extra, line)
			}
		}
		if slices.Equal(w, extra) {
			continue
		}
		for _, line := range w {
			if !slices.Contains(extra, line) {
				out = append(out, "table "+table+": missing on Postgres: "+line)
			}
		}
		for _, line := range extra {
			if !slices.Contains(w, line) {
				out = append(out, "table "+table+": only on Postgres: "+line)
			}
		}
		if len(out) == 0 || !strings.HasPrefix(out[len(out)-1], "table "+table+":") {
			out = append(out, "table "+table+": column order differs")
		}
	}
	return out
}

func keys(s shape) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	return out
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// typeClass normalizes a declared type to text, integer, real or blob.
func typeClass(decl string) string {
	switch strings.ToLower(decl) {
	case "text":
		return "text"
	case "integer", "bigint":
		return "integer"
	case "real", "double precision":
		return "real"
	case "blob", "bytea":
		return "blob"
	}
	return "other:" + decl
}

func strings1(t *testing.T, q libstore.Querier, query string, args ...any) []string {
	t.Helper()
	rows, err := q.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func sqliteShape(t *testing.T, st *libstore.Store) shape {
	t.Helper()
	ctx := context.Background()
	q := st.Reader()
	out := shape{}
	tables := strings1(t, q, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT IN ('goose_db_version','sqlite_sequence') ORDER BY name`)
	for _, table := range tables {
		var lines, pk []string
		pkPos := map[int]string{}
		rows, err := q.QueryContext(ctx, `SELECT name, type, "notnull", pk FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, typ string
			var notnull, pkn int
			if serr := rows.Scan(&name, &typ, &notnull, &pkn); serr != nil {
				t.Fatal(serr)
			}
			null := "null"
			if notnull == 1 || pkn > 0 {
				null = "not null"
			}
			class := typeClass(typ)
			if c, ok := storedAs[table+"."+name]; ok {
				class = c
			}
			lines = append(lines, "column "+name+" "+class+" "+null)
			if pkn > 0 {
				pkPos[pkn] = name
			}
		}
		_ = rows.Close()
		for i := 1; i <= len(pkPos); i++ {
			pk = append(pk, pkPos[i])
		}
		if len(pk) > 0 {
			lines = append(lines, "primary key ("+strings.Join(pk, ",")+")")
		}
		var idx []string
		rows, err = q.QueryContext(ctx, `SELECT name, "unique", origin, partial FROM pragma_index_list(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		type index struct {
			name, origin    string
			unique, partial int
		}
		var indexes []index
		for rows.Next() {
			var ix index
			if serr := rows.Scan(&ix.name, &ix.unique, &ix.origin, &ix.partial); serr != nil {
				t.Fatal(serr)
			}
			indexes = append(indexes, ix)
		}
		_ = rows.Close()
		for _, ix := range indexes {
			cols := strings.Join(strings1(t, q, `SELECT coalesce(name,'<expr>') FROM pragma_index_info(?) ORDER BY seqno`, ix.name), ",")
			switch ix.origin {
			case "pk":
			case "u":
				idx = append(idx, "unique ("+cols+")")
			default:
				idx = append(idx, indexLine(ix.name, cols, ix.unique == 1, ix.partial == 1))
			}
		}
		rows, err = q.QueryContext(ctx, `SELECT id, "table", "from", coalesce("to",'') FROM pragma_foreign_key_list(?) ORDER BY id, seq`, table)
		if err != nil {
			t.Fatal(err)
		}
		type fk struct{ table, from, to []string }
		fks := map[int]*fk{}
		var order []int
		for rows.Next() {
			var id int
			var ref, from, to string
			if err := rows.Scan(&id, &ref, &from, &to); err != nil {
				t.Fatal(err)
			}
			if fks[id] == nil {
				fks[id] = &fk{table: []string{ref}}
				order = append(order, id)
			}
			fks[id].from = append(fks[id].from, from)
			fks[id].to = append(fks[id].to, to)
		}
		_ = rows.Close()
		for _, id := range order {
			f := fks[id]
			to := f.to
			if slices.Contains(to, "") { // REFERENCES t: the referenced table's primary key
				to = sqlitePK(t, q, f.table[0])
			}
			idx = append(idx, "foreign key ("+strings.Join(f.from, ",")+") references "+f.table[0]+" ("+strings.Join(to, ",")+")")
		}
		for _, tr := range strings1(t, q, `SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name=?`, table) {
			idx = append(idx, "trigger "+tr)
		}
		sort.Strings(idx)
		out[table] = append(lines, idx...)
	}
	return out
}

func sqlitePK(t *testing.T, q libstore.Querier, table string) []string {
	t.Helper()
	return strings1(t, q, `SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, table)
}

func indexLine(name, cols string, unique, partial bool) string {
	line := "index " + name + " (" + cols + ")"
	if unique {
		line += " unique"
	}
	if partial {
		line += " partial"
	}
	return line
}

func postgresShape(t *testing.T, st *libstore.Store) shape {
	t.Helper()
	ctx := context.Background()
	q := st.Reader()
	out := shape{}
	tables := strings1(t, q, `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE' AND table_name<>'goose_db_version' ORDER BY table_name`)
	for _, table := range tables {
		var lines []string
		rows, err := q.QueryContext(ctx, `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? ORDER BY ordinal_position`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, typ, nullable string
			if serr := rows.Scan(&name, &typ, &nullable); serr != nil {
				t.Fatal(serr)
			}
			null := "null"
			if nullable == "NO" {
				null = "not null"
			}
			lines = append(lines, "column "+name+" "+typeClass(typ)+" "+null)
		}
		_ = rows.Close()
		rel := `(SELECT c.oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname=?)`
		// Constraint columns in key order.
		cols := `(SELECT string_agg(a.attname, ',' ORDER BY k.ord) FROM unnest(%s) WITH ORDINALITY k(attnum, ord) JOIN pg_attribute a ON a.attrelid=%s AND a.attnum=k.attnum)`
		if pk := strings1(t, q, `SELECT `+fmt.Sprintf(cols, "con.conkey", "con.conrelid")+` FROM pg_constraint con WHERE con.conrelid=`+rel+` AND con.contype='p'`, table); len(pk) > 0 {
			lines = append(lines, "primary key ("+pk[0]+")")
		}
		var idx []string
		for _, u := range strings1(t, q, `SELECT `+fmt.Sprintf(cols, "con.conkey", "con.conrelid")+` FROM pg_constraint con WHERE con.conrelid=`+rel+` AND con.contype='u'`, table) {
			idx = append(idx, "unique ("+u+")")
		}
		rows, err = q.QueryContext(ctx, `SELECT ic.relname, i.indisunique, i.indpred IS NOT NULL,
 (SELECT string_agg(CASE WHEN k.attnum=0 THEN '<expr>' ELSE a.attname END, ',' ORDER BY k.ord) FROM unnest(i.indkey::int2[]) WITH ORDINALITY k(attnum, ord) LEFT JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.attnum)
 FROM pg_index i JOIN pg_class ic ON ic.oid=i.indexrelid
 WHERE i.indrelid=`+rel+` AND NOT EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conindid=i.indexrelid AND con.conrelid=i.indrelid)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, columns string
			var unique, partial bool
			if err := rows.Scan(&name, &unique, &partial, &columns); err != nil {
				t.Fatal(err)
			}
			idx = append(idx, indexLine(name, columns, unique, partial))
		}
		_ = rows.Close()
		idx = append(idx, strings1(t, q, `SELECT 'foreign key ('||`+fmt.Sprintf(cols, "con.conkey", "con.conrelid")+`||') references '||(SELECT relname FROM pg_class WHERE oid=con.confrelid)||' ('||`+fmt.Sprintf(cols, "con.confkey", "con.confrelid")+`||')' FROM pg_constraint con WHERE con.conrelid=`+rel+` AND con.contype='f'`, table)...)
		for _, tr := range strings1(t, q, `SELECT tgname FROM pg_trigger WHERE tgrelid=`+rel+` AND NOT tgisinternal`, table) {
			idx = append(idx, "trigger "+tr)
		}
		sort.Strings(idx)
		out[table] = append(lines, idx...)
	}
	return out
}

// The decision log is append-only on either dialect (a trigger in each set): a row goes in, and an
// update or a delete of it is refused.
func TestDecisionLogIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	st := storetest.New(t).Open(t).Lib()
	if _, err := st.ExecContext(ctx, `INSERT INTO approval_decision_log(event_id, ts_ns, item_id, kind) VALUES (?, ?, ?, ?)`, "e1", 1, "item", "decided"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE approval_decision_log SET note='changed'`, `DELETE FROM approval_decision_log`} {
		if _, err := st.ExecContext(ctx, q); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v, want the append-only refusal", q, err)
		}
	}
	var n int
	if err := st.QueryRowContext(ctx, `SELECT count(*) FROM approval_decision_log WHERE note=''`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the row changed: %d %v", n, err)
	}
}
