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

format:
	go fmt

gobuild:
	go build -o bin/integresql ./cmd/server

lint:
	golangci-lint run --fast

bench: ##- Run tests, output by package, print coverage.
	@go test -benchmem=false -run=./... -bench . github.com/allaboutapps/integresql/tests -race -count=4 -v

# https://github.com/gotestyourself/gotestsum#format
# w/o cache https://github.com/golang/go/issues/24573 - see "go help testflag"
# note that these tests should not run verbose by default (e.g. use your IDE for this)
# TODO: add test shuffling/seeding when landed in go v1.15 (https://github.com/golang/go/issues/28592)
# tests by pkgname
test: ##- Run tests, output by package, print coverage.
	@$(MAKE) go-test-by-pkg
	@$(MAKE) go-test-print-coverage

# note that we explicitly don't want to use a -coverpkg=./... option, per pkg coverage take precedence
go-test-by-pkg: ##- (opt) Run tests, output by package.
	gotestsum --format pkgname-and-test-fails --jsonfile /tmp/test.log -- -race -cover -count=1 -coverprofile=/tmp/coverage.out ./...

go-test-print-coverage: ##- (opt) Print overall test coverage (must be done after running tests).
	@printf "coverage "
	@go tool cover -func=/tmp/coverage.out | tail -n 1 | awk '{$$1=$$1;print}'

# TODO: switch to "-m direct" after go 1.17 hits: https://github.com/golang/go/issues/40364
get-go-outdated-modules: ##- (opt) Prints outdated (direct) go modules (from go.mod).
	@((go list -u -m -f '{{if and .Update (not .Indirect)}}{{.}}{{end}}' all) 2>/dev/null | grep " ") || echo "go modules are up-to-date."


### -----------------------
# --- Initializing
### -----------------------

init: ##- Runs make modules, tools and tidy.
	@$(MAKE) modules
	@$(MAKE) tools
	@$(MAKE) tidy
	@go version

# cache go modules (locally into .pkg)
modules: ##- (opt) Cache packages as specified in go.mod.
	go mod download

# https://marcofranssen.nl/manage-go-tools-via-go-modules/
tools: ##- (opt) Install packages as specified in tools.go.
	@cat tools.go | grep _ | awk -F'"' '{print $$2}' | xargs -P $$(nproc) -tI % go install %

tidy: ##- (opt) Tidy our go.sum file.
	go mod tidy

### -----------------------
# --- SQL
### -----------------------

reset: ##- Wizard to drop and create our development database.
	@echo "DROP & CREATE database:"
	@echo "  PGHOST=${PGHOST} PGDATABASE=${PGDATABASE}" PGUSER=${PGUSER}
	@echo -n "Are you sure? [y/N] " && read ans && [ $${ans:-N} = y ]
	psql -d postgres -c 'DROP DATABASE IF EXISTS "${PGDATABASE}";'
	psql -d postgres -c 'CREATE DATABASE "${PGDATABASE}" WITH OWNER ${PGUSER} TEMPLATE "template0"'

### -----------------------
# --- Binary checks
### -----------------------

# Got license issues with some dependencies? Provide a custom lichen --config
# see https://github.com/uw-labs/lichen#config
get-licenses: ##- Prints licenses of embedded modules in the compiled bin/integresql.
	lichen bin/integresql

get-embedded-modules: ##- Prints embedded modules in the compiled bin/integresql.
	go version -m -v bin/integresql

get-embedded-modules-count: ##- (opt) Prints count of embedded modules in the compiled bin/integresql.
	go version -m -v bin/integresql | grep $$'\tdep' | wc -l


### -----------------------
# --- Helpers
### -----------------------

clean: ##- Cleans tmp folders.
	@echo "make clean"
	@rm -rf tmp/* 2> /dev/null
	@rm -rf api/tmp/* 2> /dev/null

get-module-name: ##- Prints current go module-name (pipeable).
	@echo "${GO_MODULE_NAME}"

info-module-name: ##- (opt) Prints current go module-name.
	@echo "go module-name: '${GO_MODULE_NAME}'"

get-go-ldflags: ##- (opt) Prints used -ldflags as evaluated in Makefile used in make go-build
	@echo $(LDFLAGS)

# https://gist.github.com/prwhite/8168133 - based on comment from @m000
help: ##- Show common make targets.
	@echo "usage: make <target>"
	@echo "note: use 'make help-all' to see all make targets."
	@echo ""
	@sed -e '/#\{2\}-/!d; s/\\$$//; s/:[^#\t]*/@/; s/#\{2\}- *//' $(MAKEFILE_LIST) | grep --invert "(opt)" | sort | column -t -s '@'

help-all: ##- Show all make targets.
	@echo "usage: make <target>"
	@echo "note: make targets flagged with '(opt)' are part of a main target."
	@echo ""
	@sed -e '/#\{2\}-/!d; s/\\$$//; s/:[^#\t]*/@/; s/#\{2\}- *//' $(MAKEFILE_LIST) | sort | column -t -s '@'

### -----------------------
# --- Make variables
### -----------------------

# go module name (as in go.mod)
GO_MODULE_NAME = github.com/allaboutapps/integresql

# only evaluated if required by a recipe
# http://make.mad-scientist.net/deferred-simple-variable-expansion/

# https://medium.com/the-go-journey/adding-version-information-to-go-binaries-e1b79878f6f2
ARG_COMMIT = $(eval ARG_COMMIT := $$(shell \
	(git rev-list -1 HEAD 2> /dev/null) \
	|| (echo "unknown") \
))$(ARG_COMMIT)

ARG_BUILD_DATE = $(eval ARG_BUILD_DATE := $$(shell \
	(date -Is 2> /dev/null || date 2> /dev/null || echo "unknown") \
))$(ARG_BUILD_DATE)

# https://www.digitalocean.com/community/tutorials/using-ldflags-to-set-version-information-for-go-applications
LDFLAGS = $(eval LDFLAGS := "\
-X '$(GO_MODULE_NAME)/internal/config.ModuleName=$(GO_MODULE_NAME)'\
-X '$(GO_MODULE_NAME)/internal/config.Commit=$(ARG_COMMIT)'\
-X '$(GO_MODULE_NAME)/internal/config.BuildDate=$(ARG_BUILD_DATE)'\
")$(LDFLAGS)

### -----------------------
# --- Special targets
### -----------------------

# https://www.gnu.org/software/make/manual/html_node/Special-Targets.html
# https://www.gnu.org/software/make/manual/html_node/Phony-Targets.html
# ignore matching file/make rule combinations in working-dir
.PHONY: test help

# https://unix.stackexchange.com/questions/153763/dont-stop-makeing-if-a-command-fails-but-check-exit-status
# https://www.gnu.org/software/make/manual/html_node/One-Shell.html
# required to ensure make fails if one recipe fails (even on parallel jobs) and on pipefails
.ONESHELL:

# # normal POSIX bash shell mode
# SHELL = /bin/bash
# .SHELLFLAGS = -cEeuo pipefail

# wrapped make time tracing shell, use it via MAKE_TRACE_TIME=true make <target>
# SHELL = /bin/rksh
# .SHELLFLAGS = $@