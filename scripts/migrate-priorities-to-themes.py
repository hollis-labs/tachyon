#!/usr/bin/env python3
"""Move the earlier priority statuses onto the theme lifecycle.

  draft, active -> proposed   (legacy_status keeps the old value)
  achieved      -> done       (legacy_status keeps the old value)

Nothing is ever set to approved: approval is the operator's, so a priority that
was "active" comes out as proposed and is approved from the Flight board.
Records already on the new statuses are left alone, so a second run changes
nothing. Each change appends an audit comment.

Dry run by default. --apply first copies the database (sqlite VACUUM INTO) to
<data-dir>/backups/ and refuses to continue if that fails. Writes go through
`tachyon local-data`, as the local operator, with revision checks.

  scripts/migrate-priorities-to-themes.py --data-dir ~/.local/share/tachyon [--apply]
"""
import argparse, json, os, sqlite3, subprocess, sys, time

MAP = {"draft": "proposed", "active": "proposed", "achieved": "done"}


def verb(binary, data_dir, name, body):
    env = dict(os.environ, TACHYON_DATA_DIR=data_dir)
    p = subprocess.run([binary, "local-data", "--input", "-", name], input=json.dumps(body), capture_output=True, text=True, env=env)
    if p.returncode:
        sys.exit(f"{name} failed: {p.stderr.strip()[-300:]}")
    return json.loads(p.stdout)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data-dir", default=os.path.expanduser("~/.local/share/tachyon"))
    ap.add_argument("--binary", default="tachyon")
    ap.add_argument("--db", default="themes", help="public database name; use priorities with a binary from before the rename")
    ap.add_argument("--apply", action="store_true")
    a = ap.parse_args()

    rows = verb(a.binary, a.data_dir, "planner.portfolio.list", {"db": a.db})
    plan = [(r, MAP[r["status"]]) for r in rows if r.get("status") in MAP]
    print(f"{len(rows)} records in {a.db}; {len(plan)} to change, {len(rows) - len(plan)} already on the new statuses\n")
    for r, to in sorted(plan, key=lambda x: x[0].get("legacy_id", "")):
        print(f"  {r.get('legacy_id', '-'):6} {r['status']:9} -> {to:9} {r['title']}")
    if not plan:
        return
    if not a.apply:
        print("\ndry run: nothing written. Re-run with --apply to back up and migrate.")
        return

    os.makedirs(os.path.join(a.data_dir, "backups"), exist_ok=True)
    backup = os.path.join(a.data_dir, "backups", time.strftime("before-themes-migration-%Y%m%dT%H%M%SZ.db", time.gmtime()))
    src = sqlite3.connect(f"file:{os.path.join(a.data_dir, 'tachyon.db')}?mode=ro", uri=True)
    try:
        src.execute("VACUUM INTO ?", (backup,))
    finally:
        src.close()
    os.chmod(backup, 0o600)
    print(f"\nbackup: {backup} ({os.path.getsize(backup)} bytes)")

    for r, to in plan:
        out = verb(a.binary, a.data_dir, "planner.portfolio.patch", {"db": a.db, "id": r["id"], "expected_revision": r["rev"], "patch": {"status": to, "legacy_status": r["status"]}})
        verb(a.binary, a.data_dir, "planner.portfolio.action", {"db": a.db, "id": r["id"], "expected_revision": out["rev"], "action": "comments", "arguments": {"text": f"migrated {r['status']} -> {to} (themes); not approved, the operator approves from the Flight board", "kind": "comment"}})
        print(f"  migrated {r.get('legacy_id', r['id'])}")
    print("done")


main()
