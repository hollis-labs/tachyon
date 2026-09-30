# Contributing

How a change gets from your clone into `main`. This is deliberately short: most of what you need is written closer to the thing it describes, and this page points at it rather than keeping a second copy that drifts.

## Before your first change

`README.md` has the prerequisites and the two-process dev loop (`make run` plus `make ui-dev`). Run `lefthook install` once — a tracked config installs no hooks by itself, and a clone that skips it has no commit or push checks and says nothing about it.

`AGENTS.md` is the fastest orientation to the layout: which directory does what, and the boundaries that are not obvious from reading the code. It is written for an agent working in the repo, which makes it unusually direct about the things that bite.

## The sequence

1. **Branch.** `<type>/<short-slug>`, where the type matches the change — `feat`, `fix`, `docs`, `chore`. Nothing enforces this; it is what the history does.
2. **Change one thing.** A branch carrying two unrelated changes costs the reviewer the ability to accept one and question the other.
3. **Run the checks** before you push: `make vet test`, and in `frontend/` `npm run typecheck && npm run lint`. CI runs `go vet`, `go build` and `go test` on the pull request.
4. **Push, and open a pull request** against `main`.

Commit subjects follow the conventional-commit shape — a type, an optional scope, a colon, then the summary, for example `fix(agent-ops): correct status vocabulary`. To see what is actually in use rather than trusting this sentence:

```
git log --no-merges -40 --format='%s' | grep -oE '^[a-z]+(\([a-z-]+\))?:' | sort -u
```

## What a pull request should carry

The reviewer was not there when you made the decisions. State what the change does, what it deliberately leaves alone, and the evidence that it works — the commands you ran and what came back, not a claim that it passes. For a UI change, say which screen you exercised and against which backend.

If a number appears in the description, put the command that produced it beside it.

## The one that is easy to get wrong

**The plugin pipe is serial.** Tachyon talks to each plugin over one stdin/stdout pair with no request IDs. The call lock and the single long-lived decoder in `internal/plugins/manager.go` are what keep concurrent UI requests from corrupting each other's responses; the failure shows up as random JSON decode errors under load, not as a clean test failure. Read the comments there before touching that path.

## Things that surprise people

- **`go build` does not build the plugins.** They are separate binaries spawned by relative path. Use `make all` (or `make install`), or the UI will show a plugin as missing or stale.
- **`internal/webui/dist/.gitkeep` must stay.** The embed directive fails to compile without a match, and the built bundle must not be committed beside it.
- **The UI lives under `/sysop/`**, not the root path, in both the dev server and the built binary.
- **The agent-ops plugin expects a Nanite API** at `http://localhost:8090`. Without one the pages load but have no data.

## What this does not cover

- **Which change is worth making.** There is no roadmap here by design; that conversation happens in issues.
- **Release and deployment.** Separate subjects with separate mechanics.
- **Plugin authoring in general.** A plugin is a subprocess against the published `plugin-sdk` interface, not a contribution to this repo.
