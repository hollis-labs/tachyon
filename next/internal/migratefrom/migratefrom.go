// Package migratefrom is the one-way data migration from the old Tachyon build (port plan section
// 5). It copies every row of `.backup` copies of the old tachyon.db and jobs.db into the new, empty
// database that the database module has just created, and proves source = target per table.
//
// What it guarantees, in order:
//
//  1. Input is backup files only: a source with a non-empty -wal or -journal beside it is refused, so
//     is a source directory that is the target's data directory or the old build's live one. Each
//     copy is opened read-only and immutable (mode=ro&immutable=1) and hashed before and after.
//  2. Integrity and foreign keys are checked on both sources.
//  3. The schema is enforced, not assumed: the source's every table, column, index and trigger must
//     equal the new schema (internal/store, the old schema at 056); an unknown object is a failure,
//     never a skip. The only exception is an empty rebuild leftover (*_next, *_new, *_old, *_NNN),
//     which is dropped and reported; a leftover with rows fails. The old ledger must hold exactly the
//     56 migration names this build knows. jobs.db must hold exactly the scheduler tables that the
//     pinned libs sqlstore creates.
//  4. Every schedule's job type is one the new build registers, and every schedule id is the hash of
//     the scope recorded for it. Each scope's upstream address must equal the configured upstream
//     byte for byte; an unconfigured upstream fails too, unless a rehearsal allows it explicitly.
//  5. The target holds no rows.
//  6. The copy runs in SQL, never through Go values: both copies are ATTACHed and each table is
//     copied with INSERT ... SELECT in foreign-key order, in one transaction, foreign keys checked at
//     commit. sqlite_sequence is copied verbatim, so no id is ever reused.
//  7. Per table, the row count and a checksum over every column of every row, typeof() included,
//     computed in SQL on both sides in primary-key order, must be equal; so must sqlite_sequence. The
//     target's integrity, foreign keys and schema version are checked, and the sources' hashes must
//     be unchanged.
//
// A report (JSON) is written whether the run succeeds or not. On a failure after the copy, the target
// is left as it is for inspection.
package migratefrom

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	libstore "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/util/scheduler/sqlstore"
	_ "modernc.org/sqlite" // the "sqlite" driver, the one the database module uses

	"github.com/hollis-labs/tachyon/next/internal/schema"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// The files of a source directory, and the report's default name.
const (
	TachyonFile = "tachyon.db"
	JobsFile    = "jobs.db"
	ReportFile  = "migrate-from-report.json"
	// FilesReportFile is a --files-only run's report, kept apart from the migration's own.
	FilesReportFile = "migrate-from-files-report.json"
)

// Options are one run's inputs.
type Options struct {
	// Source is the directory holding the .backup copies of tachyon.db and jobs.db.
	Source string
	// Refused are directories that must never be the source: the target's data directory and the
	// old build's live data directory.
	Refused []string
	// JobTypes are the scheduler job types the new build registers.
	JobTypes []string
	// Instances maps a schedule scope's provider (tether, github, cerberus) to the configured
	// upstream address. A schedule whose provider has no configured address fails the run: an
	// unchecked scope is unknown, and unknown means fail (plan section 5).
	Instances map[string]string
	// AllowUnchecked reports such a schedule as not checked instead of failing. For rehearsals,
	// which run with every upstream blank; never for the cutover.
	AllowUnchecked bool
	// WorkspaceTarget is where the chat workspace files go (tachyon.workspace_dir). Empty: the file
	// step is not run (tests of the database copy alone).
	WorkspaceTarget string
	// FilesOnly runs only the file step, on a target an earlier run migrated: Migrated must hold that
	// run's successful report. A rerun over a complete target copies nothing.
	FilesOnly bool
	// Migrated is the earlier run's report file, read by FilesOnly (never written).
	Migrated string
	// Report is the report file. Empty: no file is written (tests).
	Report string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Report is what a run found and did.
type Report struct {
	Started       time.Time        `json:"started"`
	Finished      time.Time        `json:"finished"`
	DurationMS    int64            `json:"duration_ms"`
	OK            bool             `json:"ok"`
	Error         string           `json:"error,omitempty"`
	SourceDir     string           `json:"source_dir"`
	SourceFiles   []FileEntry      `json:"source_dir_files"`
	Sources       []SourceReport   `json:"sources"`
	Ledger        int              `json:"ledger_names"`
	Dropped       []string         `json:"dropped_leftovers"`
	Schedules     []ScheduleReport `json:"schedules"`
	Tables        []TableReport    `json:"tables"`
	Sequence      []SequenceRow    `json:"sqlite_sequence"`
	Target        TargetReport     `json:"target"`
	SourcesStable bool             `json:"source_hashes_unchanged"`
	// Files is the file-store step (the chat workspace files).
	Files *FilesReport `json:"files,omitempty"`
	// FilesOnly marks a run of the file step alone.
	FilesOnly bool `json:"files_only,omitempty"`
	// AllowUnchecked records --allow-unchecked-instances: a rehearsal run, never a cutover.
	AllowUnchecked bool `json:"allow_unchecked_instances"`
}

// FileEntry is one file in the source directory.
type FileEntry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}

// SourceReport is one source database.
type SourceReport struct {
	File        string `json:"file"`
	SHA256      string `json:"sha256_before"`
	SHA256After string `json:"sha256_after"`
	Integrity   string `json:"integrity_check"`
	FKViolation int    `json:"foreign_key_violations"`
}

// ScheduleReport is one scheduler schedule.
type ScheduleReport struct {
	ID             string `json:"id"`
	JobType        string `json:"job_type"`
	Enabled        bool   `json:"enabled"`
	Scope          string `json:"scope"`
	IDMatchesScope bool   `json:"id_matches_scope"`
	Instance       string `json:"instance_check"`
}

// TableReport is one copied table.
type TableReport struct {
	Name           string `json:"name"`
	From           string `json:"from"`
	Rule           string `json:"rule"`
	SourceRows     int64  `json:"source_rows"`
	TargetRows     int64  `json:"target_rows"`
	SourceChecksum string `json:"source_checksum"`
	TargetChecksum string `json:"target_checksum"`
	Equal          bool   `json:"equal"`
}

// SequenceRow is one sqlite_sequence entry on both sides.
type SequenceRow struct {
	Name   string `json:"name"`
	Source int64  `json:"source"`
	Target int64  `json:"target"`
}

// TargetReport is the target after the copy.
type TargetReport struct {
	Integrity    string `json:"integrity_check"`
	FKViolation  int    `json:"foreign_key_violations"`
	GooseVersion int64  `json:"goose_version"`
}

// leftover matches the names a table rebuild leaves behind.
var leftover = regexp.MustCompile(`_(next|new|old|tmp)$|_[0-9]{3}$`)

// framework reports a table that the framework owns in the target: goose's version table, the
// identity module's, the scheduler's (copied from jobs.db, checked separately) and sqlite_sequence.
func framework(table string) bool {
	return table == "goose_db_version" || table == "sqlite_sequence" ||
		strings.HasPrefix(table, "identity_") || strings.HasPrefix(table, "gosched_")
}

// Run migrates o.Source into the database behind st, which the database and scheduler modules have
// just created and migrated. The report is returned (and written) on success and on failure.
func Run(ctx context.Context, st *libstore.Store, o Options) (*Report, error) {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	r := &Report{Started: now().UTC(), SourceDir: o.Source, AllowUnchecked: o.AllowUnchecked, FilesOnly: o.FilesOnly}
	var err error
	if o.FilesOnly {
		err = runFiles(ctx, st, o, r)
	} else {
		err = run(ctx, st, o, r)
	}
	r.Finished = now().UTC()
	r.DurationMS = r.Finished.Sub(r.Started).Milliseconds()
	r.OK = err == nil
	if err != nil {
		r.Error = err.Error()
	}
	if o.Report != "" {
		if werr := writeReport(o.Report, r); werr != nil {
			err = errors.Join(err, fmt.Errorf("write the report: %w", werr))
		}
	}
	return r, err
}

func run(ctx context.Context, st *libstore.Store, o Options, r *Report) error {
	if st == nil {
		return errors.New("migrate-from: the database module is not started")
	}
	srcDir, err := checkSourceDir(o.Source, o.Refused)
	if err != nil {
		return err
	}
	r.SourceDir = srcDir
	files := map[string]string{TachyonFile: filepath.Join(srcDir, TachyonFile), JobsFile: filepath.Join(srcDir, JobsFile)}
	for _, name := range []string{TachyonFile, JobsFile} {
		if e := checkBackupFile(files[name]); e != nil {
			return e
		}
	}
	if r.SourceFiles, err = listDir(srcDir); err != nil {
		return err
	}

	// 1, 2: open read-only and immutable, hash, integrity and foreign keys.
	src, err := openSource(files[TachyonFile])
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	jobs, err := openSource(files[JobsFile])
	if err != nil {
		return err
	}
	defer func() { _ = jobs.Close() }()
	for _, s := range []struct {
		name string
		db   *sql.DB
	}{{TachyonFile, src}, {JobsFile, jobs}} {
		sr := SourceReport{File: s.name}
		if sr.SHA256, err = fileSHA256(files[s.name]); err != nil {
			return err
		}
		if sr.Integrity, sr.FKViolation, err = checks(ctx, s.db); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		r.Sources = append(r.Sources, sr)
		if sr.Integrity != "ok" || sr.FKViolation != 0 {
			return fmt.Errorf("%s: integrity_check %q, %d foreign key violation(s)", s.name, sr.Integrity, sr.FKViolation)
		}
	}

	// 3: the schema, the ledger and the scheduler tables.
	// The ledger first: a source from an older or newer old build is named as such, before its schema
	// is compared.
	if r.Ledger, err = checkLedger(ctx, src); err != nil {
		return err
	}
	// The reference is the old schema (the init migration and the carried old migrations, 001..058).
	// The target holds it, plus the tables the new build adds, which the old data has no rows for.
	target, err := initSchema(ctx)
	if err != nil {
		return err
	}
	inTarget, err := schema.Describe(ctx, st.Reader(), framework)
	if err != nil {
		return err
	}
	for name, o := range target {
		if d := schema.Diff(schema.Schema{name: o}, schema.Schema{name: inTarget[name]}); inTarget[name].Type == "" || len(d) > 0 {
			return fmt.Errorf("target: %s %s is not the old schema's (%v)", o.Type, name, d)
		}
	}
	source, err := schema.Describe(ctx, src, func(t string) bool { return t == "sqlite_sequence" })
	if err != nil {
		return err
	}
	if r.Dropped, err = dropLeftovers(ctx, src, source, target); err != nil {
		return err
	}
	if d := schema.Diff(target, source); len(d) > 0 {
		return fmt.Errorf("%s: the schema is not the one this build knows (%d difference(s)); nothing was copied:\n%s", TachyonFile, len(d), strings.Join(d, "\n"))
	}
	if err = checkJobsSchema(ctx, jobs, st); err != nil {
		return err
	}

	// 4: schedules.
	if r.Schedules, err = checkSchedules(ctx, src, jobs, o.JobTypes, o.Instances, o.AllowUnchecked); err != nil {
		return err
	}

	// 5: the chat workspace files are all there, before a row is copied.
	if o.WorkspaceTarget != "" {
		if _, err = CheckWorkspace(ctx, src, filepath.Join(srcDir, WorkspaceDir), o.WorkspaceTarget); err != nil {
			return err
		}
	}

	// 5: the target is empty.
	tables := copyOrder(target)
	jobTables, err := jobsTables(ctx, jobs)
	if err != nil {
		return err
	}
	if err = requireEmpty(ctx, st, append(inTarget.Tables(), jobTables...)); err != nil {
		return err
	}

	// 6: copy.
	if err = copyAll(ctx, st, files, tables, jobTables, target); err != nil {
		return err
	}

	// 7: verify.
	if err = verify(ctx, st, src, jobs, tables, jobTables, r); err != nil {
		return err
	}
	stable := true
	for i, name := range []string{TachyonFile, JobsFile} {
		after, err := fileSHA256(files[name])
		if err != nil {
			return err
		}
		r.Sources[i].SHA256After = after
		stable = stable && after == r.Sources[i].SHA256
	}
	r.SourcesStable = stable
	if !stable {
		return errors.New("a source file changed during the run")
	}

	// 8: the chat workspace files, checked against the migrated rows.
	return copyFiles(ctx, st, o, srcDir, r)
}

// copyFiles runs the file step against the target's rows.
func copyFiles(ctx context.Context, st *libstore.Store, o Options, srcDir string, r *Report) error {
	if o.WorkspaceTarget == "" {
		return nil
	}
	fr, err := CopyWorkspace(ctx, st.Reader(), filepath.Join(srcDir, WorkspaceDir), o.WorkspaceTarget)
	r.Files = &fr
	return err
}

// runFiles is the file step alone, on a target an earlier run migrated (its report says so).
func runFiles(ctx context.Context, st *libstore.Store, o Options, r *Report) error {
	if st == nil {
		return errors.New("migrate-from: the database module is not started")
	}
	srcDir, err := checkSourceDir(o.Source, o.Refused)
	if err != nil {
		return err
	}
	r.SourceDir = srcDir
	if o.Migrated == "" {
		return errors.New("--files-only needs the report of the run that migrated this target")
	}
	b, err := os.ReadFile(o.Migrated)
	if err != nil {
		return fmt.Errorf("--files-only runs only on a target migrate-from has migrated; its report %s: %w", o.Migrated, err)
	}
	var earlier Report
	if err = json.Unmarshal(b, &earlier); err != nil || !earlier.OK || earlier.FilesOnly {
		return fmt.Errorf("--files-only runs only on a target migrate-from has migrated: %s is not a successful migration report", o.Migrated)
	}
	if o.WorkspaceTarget == "" {
		return errors.New("the workspace target directory (tachyon.workspace_dir) is not set")
	}
	return copyFiles(ctx, st, o, srcDir, r)
}

// checkSourceDir resolves the source directory and refuses a refused one.
func checkSourceDir(dir string, refused []string) (string, error) {
	if dir == "" {
		return "", errors.New("--source is required: the directory holding the .backup copies of tachyon.db and jobs.db")
	}
	abs, err := realDir(dir)
	if err != nil {
		return "", fmt.Errorf("source %s: %w", dir, err)
	}
	for _, ref := range refused {
		if ref == "" {
			continue
		}
		r, err := realDir(ref)
		if err != nil {
			continue // a refused directory that does not exist cannot be the source
		}
		if r == abs {
			return "", fmt.Errorf("source %s is %s, a data directory this command must never read: copy the files with sqlite3 \".backup\" into another directory", dir, ref)
		}
	}
	return abs, nil
}

func realDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// checkBackupFile refuses a missing file and one with a non-empty -wal or -journal beside it: pages
// that live only in a WAL or a hot journal would be missed by a read-only, immutable open.
func checkBackupFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("source %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("source %s is not a regular file", path)
	}
	for _, side := range []string{"-wal", "-journal"} {
		if fi, err := os.Stat(path + side); err == nil && fi.Size() > 0 {
			return fmt.Errorf("source %s has a non-empty %s beside it: it is a live database, not a backup copy; make one with sqlite3 \".backup\"", path, side)
		}
	}
	return nil
}

// openSource opens a backup copy read-only and immutable: no lock, no journal, nothing written.
func openSource(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", roURI(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func roURI(path string) string {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}
	return u.String()
}

func checks(ctx context.Context, q schema.Querier) (integrity string, fk int, err error) {
	rows, err := q.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return "", 0, err
	}
	var lines []string
	for rows.Next() {
		var s string
		if e := rows.Scan(&s); e != nil {
			_ = rows.Close()
			return "", 0, e
		}
		lines = append(lines, s)
	}
	if e := rows.Close(); e != nil {
		return "", 0, e
	}
	rows, err = q.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return "", 0, err
	}
	for rows.Next() {
		fk++
	}
	return strings.Join(lines, "; "), fk, rows.Close()
}

// dropLeftovers takes the empty rebuild leftovers out of the source schema and refuses one with rows.
func dropLeftovers(ctx context.Context, src *sql.DB, source, target schema.Schema) ([]string, error) {
	var dropped []string
	for _, t := range source.Tables() {
		if _, known := target[t]; known || !leftover.MatchString(t) {
			continue
		}
		n, err := count(ctx, src, t)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, fmt.Errorf("%s: rebuild leftover %s holds %d row(s); it must be empty to be dropped", TachyonFile, t, n)
		}
		dropped = append(dropped, t)
		for name, o := range source {
			if o.Table == t {
				delete(source, name)
			}
		}
	}
	return dropped, nil
}

func checkLedger(ctx context.Context, src *sql.DB) (int, error) {
	rows, err := src.QueryContext(ctx, `SELECT name FROM tachyon_migrations ORDER BY name`)
	if err != nil {
		return 0, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return 0, err
		}
		names = append(names, n)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if !slices.Equal(names, ledgerNames) {
		return 0, fmt.Errorf("%s: tachyon_migrations holds %d name(s), not exactly the %d this build was made for (%s .. %s); a source from an older or newer old build is refused, never guessed", TachyonFile, len(names), len(ledgerNames), ledgerNames[0], ledgerNames[len(ledgerNames)-1])
	}
	return len(names), nil
}

// checkJobsSchema compares the source's and the target's scheduler tables with what the pinned
// sqlstore creates. jobs.db has no version marker: the table set and its SQL text are the version.
func checkJobsSchema(ctx context.Context, jobs *sql.DB, st *libstore.Store) error {
	ref, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer func() { _ = ref.Close() }()
	ref.SetMaxOpenConns(1)
	if err = sqlstore.Migrate(ctx, ref); err != nil {
		return err
	}
	want, err := schema.Describe(ctx, ref, func(t string) bool { return t == "sqlite_sequence" })
	if err != nil {
		return err
	}
	got, err := schema.Describe(ctx, jobs, func(t string) bool { return t == "sqlite_sequence" })
	if err != nil {
		return err
	}
	if d := schema.Diff(want, got); len(d) > 0 {
		return fmt.Errorf("%s: the scheduler schema is not the one the pinned sqlstore creates (%d difference(s)); nothing was copied:\n%s", JobsFile, len(d), strings.Join(d, "\n"))
	}
	inTarget, err := schema.Describe(ctx, st.Reader(), func(t string) bool { return !strings.HasPrefix(t, "gosched_") })
	if err != nil {
		return err
	}
	if d := schema.Diff(want, inTarget); len(d) > 0 {
		return fmt.Errorf("target: the scheduler tables differ from the pinned sqlstore's (%d difference(s)):\n%s", len(d), strings.Join(d, "\n"))
	}
	return nil
}

// scope is the part of an object-sync scope the instance check reads.
type scope struct {
	Provider string `json:"provider"`
	Instance string `json:"instance"`
}

// checkSchedules refuses a schedule whose job type is not registered, and one whose id is not the
// hash of the scope recorded for it (the old build's sourceID: "object-sync-" + the first 16 bytes
// of sha256 of the scope JSON, hex). With an upstream configured, the scope's instance must equal it
// byte for byte, or the new build's sync would compute other ids and orphan the copied schedules.
func checkSchedules(ctx context.Context, src, jobs *sql.DB, types []string, instances map[string]string, allowUnchecked bool) ([]ScheduleReport, error) {
	rows, err := jobs.QueryContext(ctx, `SELECT id, job_type, enabled FROM gosched_schedules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []ScheduleReport
	for rows.Next() {
		var s ScheduleReport
		var enabled int64
		if e := rows.Scan(&s.ID, &s.JobType, &enabled); e != nil {
			_ = rows.Close()
			return nil, e
		}
		s.Enabled = enabled != 0
		out = append(out, s)
	}
	if e := rows.Close(); e != nil {
		return nil, e
	}
	scopes := map[string]string{}
	rows, err = src.QueryContext(ctx, `SELECT source_id, scope_json FROM object_sync_state`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, sc string
		if err := rows.Scan(&id, &sc); err != nil {
			_ = rows.Close()
			return nil, err
		}
		scopes[id] = sc
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var errs []error
	for i, s := range out {
		if !slices.Contains(types, s.JobType) {
			errs = append(errs, fmt.Errorf("schedule %s: job type %q is not registered by this build", s.ID, s.JobType))
		}
		sc, ok := scopes[s.ID]
		if !ok {
			errs = append(errs, fmt.Errorf("schedule %s: no scope recorded in object_sync_state", s.ID))
			continue
		}
		s.Scope = sc
		sum := sha256.Sum256([]byte(sc))
		s.IDMatchesScope = s.ID == "object-sync-"+hex.EncodeToString(sum[:16])
		if !s.IDMatchesScope {
			errs = append(errs, fmt.Errorf("schedule %s: the id is not the hash of its recorded scope %s", s.ID, sc))
		}
		var parsed scope
		if err := json.Unmarshal([]byte(sc), &parsed); err != nil {
			errs = append(errs, fmt.Errorf("schedule %s: scope %s: %w", s.ID, sc, err))
		}
		switch want := instances[parsed.Provider]; want {
		case "":
			s.Instance = "not checked: upstream " + parsed.Provider + " not configured"
			if !allowUnchecked {
				errs = append(errs, fmt.Errorf("schedule %s: upstream %s is not configured, so its scope instance %q cannot be checked; configure tachyon.upstreams.%s.url exactly as the cutover will (--allow-unchecked-instances is for rehearsals only)", s.ID, parsed.Provider, parsed.Instance, parsed.Provider))
			}
		case parsed.Instance:
			s.Instance = "equal to the configured " + parsed.Provider + " upstream"
		default:
			s.Instance = fmt.Sprintf("differs: configured %q", want)
			errs = append(errs, fmt.Errorf("schedule %s: the scope's %s instance %q is not the configured upstream %q byte for byte; the new build would compute other schedule ids", s.ID, parsed.Provider, parsed.Instance, want))
		}
		out[i] = s
	}
	return out, errors.Join(errs...)
}

func jobsTables(ctx context.Context, jobs *sql.DB) ([]string, error) {
	s, err := schema.Describe(ctx, jobs, func(t string) bool { return t == "sqlite_sequence" })
	if err != nil {
		return nil, err
	}
	return s.Tables(), nil
}

// copyOrder is the target's tables in foreign-key order (a referenced table before the tables that
// reference it), ties by name.
func copyOrder(target schema.Schema) []string {
	deps := map[string][]string{}
	for _, t := range target.Tables() {
		for _, fk := range target[t].ForeignKeys {
			parts := strings.Split(fk, "|") // id|seq|table|from|to|on_update|on_delete|match
			if len(parts) > 2 && parts[2] != t {
				deps[t] = append(deps[t], parts[2])
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	var visit func(string)
	visit = func(t string) {
		if seen[t] {
			return
		}
		seen[t] = true
		for _, d := range deps[t] {
			visit(d)
		}
		out = append(out, t)
	}
	for _, t := range target.Tables() {
		visit(t)
	}
	return out
}

func requireEmpty(ctx context.Context, st *libstore.Store, tables []string) error {
	for _, t := range tables {
		n, err := count(ctx, st.Reader(), t)
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("target: table %s already holds %d row(s); migrate-from copies only into a new, empty data directory", t, n)
		}
	}
	return nil
}

func count(ctx context.Context, q schema.Querier, table string) (int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT count(*) FROM `+quoteIdent(table)) //nolint:gosec // table names come from sqlite_master and are quoted
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var n int64
	if rows.Next() {
		err = rows.Scan(&n)
	}
	return n, errors.Join(err, rows.Err())
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// copyAll copies every table in SQL on the store's one write connection. ATTACH cannot run inside a
// transaction, so this pins the connection itself rather than using WriteTx: ATTACH both copies, then
// one BEGIN IMMEDIATE .. COMMIT with foreign keys deferred to commit, then DETACH. Nothing else
// writes during a migrate-from (no other module that writes is started).
func copyAll(ctx context.Context, st *libstore.Store, files map[string]string, tables, jobTables []string, target schema.Schema) (err error) {
	conn, err := st.DB().Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	exec := func(q string, args ...any) error {
		_, e := conn.ExecContext(ctx, q, args...)
		if e != nil {
			return fmt.Errorf("%w (in %.120s)", e, q)
		}
		return nil
	}
	if err = exec(`ATTACH DATABASE ? AS src`, roURI(files[TachyonFile])); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, exec(`DETACH DATABASE src`)) }()
	if err = exec(`ATTACH DATABASE ? AS jobs`, roURI(files[JobsFile])); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, exec(`DETACH DATABASE jobs`)) }()

	if err = exec(`BEGIN IMMEDIATE`); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, rbErr := conn.ExecContext(context.Background(), `ROLLBACK`)
			err = errors.Join(err, rbErr)
		}
	}()
	if err = exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	copyTable := func(schemaName, table string, cols []string) error {
		list := strings.Join(cols, ", ")
		return exec(`INSERT INTO main.` + quoteIdent(table) + ` (` + list + `) SELECT ` + list + ` FROM ` + schemaName + `.` + quoteIdent(table))
	}
	for _, t := range tables {
		if err = copyTable("src", t, columnNames(target[t])); err != nil {
			return err
		}
	}
	for _, t := range jobTables {
		cols, e := tableColumns(ctx, conn, "jobs", t)
		if e != nil {
			return e
		}
		if err = copyTable("jobs", t, cols); err != nil {
			return err
		}
	}
	// The source's high-water marks replace the ones the inserts left (max(id)), so an id deleted at
	// the top of a table is never handed out again.
	if err = exec(`DELETE FROM main.sqlite_sequence WHERE name IN (SELECT name FROM src.sqlite_sequence)`); err != nil {
		return err
	}
	if err = exec(`INSERT INTO main.sqlite_sequence (name, seq) SELECT name, seq FROM src.sqlite_sequence`); err != nil {
		return err
	}
	if err = exec(`COMMIT`); err != nil {
		return err
	}
	committed = true
	return nil
}

// columnNames returns a table's quoted column names, in declaration order, from its table_info rows.
func columnNames(o schema.Object) []string {
	var out []string
	for _, c := range o.Columns {
		out = append(out, quoteIdent(strings.Split(c, "|")[1]))
	}
	return out
}

func tableColumns(ctx context.Context, conn *sql.Conn, schemaName, table string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT name FROM pragma_table_info(?, ?)`, table, schemaName)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, quoteIdent(n))
	}
	return out, rows.Close()
}

// verify compares every copied table and sqlite_sequence, and checks the target.
func verify(ctx context.Context, st *libstore.Store, src, jobs *sql.DB, tables, jobTables []string, r *Report) error {
	var mismatches []string
	compare := func(from string, q schema.Querier, t string) error {
		srcRows, srcSum, err := Checksum(ctx, q, t)
		if err != nil {
			return fmt.Errorf("checksum source %s: %w", t, err)
		}
		dstRows, dstSum, err := Checksum(ctx, st.Reader(), t)
		if err != nil {
			return fmt.Errorf("checksum target %s: %w", t, err)
		}
		tr := TableReport{Name: t, From: from, Rule: "copy", SourceRows: srcRows, TargetRows: dstRows,
			SourceChecksum: srcSum, TargetChecksum: dstSum, Equal: srcRows == dstRows && srcSum == dstSum}
		if t == "tachyon_migrations" {
			tr.Rule = "ledger (copied as history)"
		}
		if !tr.Equal {
			mismatches = append(mismatches, fmt.Sprintf("%s: %d rows %s, target %d rows %s", t, srcRows, srcSum, dstRows, dstSum))
		}
		r.Tables = append(r.Tables, tr)
		return nil
	}
	for _, t := range tables {
		if err := compare(TachyonFile, src, t); err != nil {
			return err
		}
	}
	for _, t := range jobTables {
		if err := compare(JobsFile, jobs, t); err != nil {
			return err
		}
	}
	srcSeq, err := sequence(ctx, src)
	if err != nil {
		return err
	}
	dstSeq, err := sequence(ctx, st.Reader())
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(srcSeq) {
		row := SequenceRow{Name: name, Source: srcSeq[name], Target: dstSeq[name]}
		r.Sequence = append(r.Sequence, row)
		if row.Source != row.Target {
			mismatches = append(mismatches, fmt.Sprintf("sqlite_sequence %s: source %d, target %d", name, row.Source, row.Target))
		}
	}
	if r.Target.Integrity, r.Target.FKViolation, err = checks(ctx, st.Reader()); err != nil {
		return err
	}
	if r.Target.Integrity != "ok" || r.Target.FKViolation != 0 {
		mismatches = append(mismatches, fmt.Sprintf("target: integrity_check %q, %d foreign key violation(s)", r.Target.Integrity, r.Target.FKViolation))
	}
	rows, err := st.Reader().QueryContext(ctx, `SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied`)
	if err != nil {
		return err
	}
	if rows.Next() {
		err = rows.Scan(&r.Target.GooseVersion)
	}
	if err = errors.Join(err, rows.Close()); err != nil {
		return err
	}
	head, err := store.HeadVersion()
	if err != nil {
		return err
	}
	if r.Target.GooseVersion != head {
		mismatches = append(mismatches, fmt.Sprintf("target: goose version %d, want %d", r.Target.GooseVersion, head))
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("verification failed; the target is left as it is for inspection:\n%s", strings.Join(mismatches, "\n"))
	}
	return nil
}

func sequence(ctx context.Context, q schema.Querier) (map[string]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, seq FROM sqlite_sequence WHERE name <> 'goose_db_version'`)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for rows.Next() {
		var n string
		var s int64
		if err := rows.Scan(&n, &s); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[n] = s
	}
	return out, rows.Close()
}

func sortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Checksum returns a table's row count and a sha256 over every row in primary-key order (every
// column when the table has no primary key). Each row is rendered in SQL: per column its typeof()
// and its value as SQL text (quote(); hex() for a blob; printf %!.17g for a real, which round-trips
// a double), so a value that changed only its storage class (1 and '1') changes the checksum.
func Checksum(ctx context.Context, q schema.Querier, table string) (int64, string, error) {
	cols, err := schema.Pragma(ctx, q, "table_info", table)
	if err != nil {
		return 0, "", err
	}
	if len(cols) == 0 {
		return 0, "", fmt.Errorf("table %s not found", table)
	}
	type col struct {
		name string
		pk   string
	}
	var all []col
	for _, c := range cols {
		f := strings.Split(c, "|") // cid|name|type|notnull|dflt|pk
		all = append(all, col{name: quoteIdent(f[1]), pk: f[5]})
	}
	var exprs, order []string
	for _, c := range all {
		exprs = append(exprs, `typeof(`+c.name+`) || ':' || CASE typeof(`+c.name+`) WHEN 'real' THEN printf('%!.17g', `+c.name+`) WHEN 'blob' THEN hex(`+c.name+`) ELSE quote(`+c.name+`) END`)
	}
	pks := slices.Clone(all)
	pks = slices.DeleteFunc(pks, func(c col) bool { return c.pk == "0" })
	sort.Slice(pks, func(i, j int) bool { return pks[i].pk < pks[j].pk })
	for _, c := range pks {
		order = append(order, c.name)
	}
	if len(order) == 0 {
		for _, c := range all {
			order = append(order, c.name)
		}
	}
	query := `SELECT ` + strings.Join(exprs, ` || '|' || `) + ` FROM ` + quoteIdent(table) + ` ORDER BY ` + strings.Join(order, ", ")
	rows, err := q.QueryContext(ctx, query) //nolint:gosec // identifiers come from sqlite_master and are quoted
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	var n int64
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			_ = rows.Close()
			return 0, "", err
		}
		_, _ = io.WriteString(h, line)
		_, _ = io.WriteString(h, "\n")
		n++
	}
	if err := rows.Close(); err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // a source file named by the operator
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// listDir lists every regular file in dir with its size, mode and hash.
func listDir(dir string) ([]FileEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []FileEntry
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		fe := FileEntry{Name: e.Name(), Size: fi.Size(), Mode: fi.Mode().String()}
		if fi.Mode().IsRegular() {
			if fe.SHA256, err = fileSHA256(filepath.Join(dir, e.Name())); err != nil {
				return nil, err
			}
		}
		out = append(out, fe)
	}
	return out, nil
}

func writeReport(path string, r *Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// initSchema describes the old schema (store.OldSchemaFiles: the init migration and the carried old
// migrations) built in memory.
func initSchema(ctx context.Context) (schema.Schema, error) {
	body, err := store.OldSchemaSQL()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, body); err != nil {
		return nil, fmt.Errorf("build the reference schema: %w", err)
	}
	return schema.Describe(ctx, db, framework)
}

// CheckTargetDir refuses a data directory that exists and is not empty: migrate-from copies only into
// a new one. It is called before anything is created there.
func CheckTargetDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("target data directory %s is not empty; migrate-from copies only into a new, empty data directory", dir)
	}
	return nil
}
