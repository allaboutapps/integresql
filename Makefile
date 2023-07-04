### -----------------------
# --- Building
### -----------------------

# first is default target when running "make" without args
build: ##- Default 'make' target: go-format, go-build and lint.
	@$(MAKE) format
	@$(MAKE) gobuild
	@$(MAKE) lint

# useful to ensure that everything gets resetuped from scratch
all: clean init ##- Runs all of our common make targets: clean, init, build and test.
	@$(MAKE) build
	@$(MAKE) test

info: info-go ##- Prints additional info

info-go: ##- (opt) Prints go.mod updates, module-name and current go version.
	@echo "[go.mod]" > tmp/.info-go
	@$(MAKE) get-go-outdated-modules >> tmp/.info-go
	@$(MAKE) info-module-name >> tmp/.info-go
	@go version >> tmp/.info-go
	@cat tmp/.info-go

# TODO: switch to "-m direct" after go 1.17 hits: https://github.com/golang/go/issues/40364
get-go-outdated-modules: ##- (opt) Prints outdated (direct) go modules (from go.mod).
	@((go list -u -m -f '{{if and .Update (not .Indirect)}}{{.}}{{end}}' all) 2>/dev/null | grep " ") || echo "go modules are up-to-date."

info-module-name: ##- (opt) Prints current go module-name.
	@echo "go module-name: '${GO_MODULE_NAME}'"


format:
	go fmt

gobuild:
	go build -o bin/integresql ./cmd/server

lint:
	golangci-lint run --fast

# https://github.com/golang/go/issues/24573
# w/o cache - see "go help testflag"
# use https://github.com/kyoh86/richgo to color
# note that these tests should not run verbose by default (e.g. use your IDE for this)
# TODO: add test shuffling/seeding when landed in go v1.15 (https://github.com/golang/go/issues/28592)
test:
	richgo test -cover -race -count=1 ./...

init: modules tools tidy
	@go version

# cache go modules (locally into .pkg)
modules:
	go mod download

# https://marcofranssen.nl/manage-go-tools-via-go-modules/
tools:
	cat tools.go | grep _ | awk -F'"' '{print $$2}' | xargs -tI % go install %

tidy:
	go mod tidy

clean:
	rm -rf bin

reset:
	@echo "DROP & CREATE database:"
	@echo "  PGHOST=${PGHOST} PGDATABASE=${PGDATABASE}" PGUSER=${PGUSER}
	@echo -n "Are you sure? [y/N] " && read ans && [ $${ans:-N} = y ]
	psql -d postgres -c 'DROP DATABASE IF EXISTS "${PGDATABASE}";'
	psql -d postgres -c 'CREATE DATABASE "${PGDATABASE}" WITH OWNER ${PGUSER} TEMPLATE "template0"'

# https://www.gnu.org/software/make/manual/html_node/Phony-Targets.html
# ignore matching file/make rule combinations in working-dir
.PHONY: test
