.PHONY: all build plugins run install ui-build ui-dev deps test vet clean

PLUGINS := $(notdir $(wildcard plugins/*))

# Frontend, plugin binaries, then the host binary with the UI embedded.
all: ui-build plugins build

# Install workspace dependencies (frontend, packages/*, plugins/*/ui).
deps:
	npm ci

# Build the UI (host shell + compiled-in plugin UIs) into internal/webui/dist.
ui-build: deps
	npm run build

# Vite dev server on 127.0.0.1:5179, proxying /api to the Go host on :18093
# (override with TACHYON_API_ORIGIN). Run `make run` in another shell.
ui-dev:
	npm run dev

# Build each first-party plugin backend to bin/plugins/<id>.
plugins:
	@mkdir -p bin/plugins
	@for p in $(PLUGINS); do echo "plugin $$p"; go build -o bin/plugins/$$p ./plugins/$$p || exit 1; done

# Build the host. Embeds whatever is in internal/webui/dist.
build:
	go build -o tachyon ./cmd/tachyon

run: plugins build
	./tachyon

install: ui-build plugins
	go install ./cmd/tachyon

test:
	go test ./...
	npm test

vet:
	go vet ./...

# Keeps internal/webui/dist/.gitkeep so //go:embed still compiles.
clean:
	rm -rf tachyon bin node_modules
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
