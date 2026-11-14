# Tachyon on Substrate (`next/`)

The Substrate port of Tachyon, built beside the old tree on branch `substrate-port` and promoted to the repository
root at the end of the port. Plan: `../docs/substrate-port-plan.md`.

```sh
go test -race ./...                      # unit and in-process boot tests
golangci-lint run ./...
go tool substrate pins check --app .     # go.mod against the Substrate BOM
go build -o /tmp/tachyon ./cmd/tachyon
```

Running it needs a data directory of its own (`TACHYON_DATA_DIR`, never the old build's) and an owner created
offline first: `printf '%s\n' "$PASSWORD" | tachyon identity setup --username owner`.

- `docs/architecture.md`: layout, auth model, data and configuration
- `docs/api-contract.md`: the API the SPA builds against
- `docs/conversion-notes.md`: old to new, and the open gaps
