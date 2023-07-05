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

# https://github.com/golang/go/issues/24573
# w/o cache - see "go help testflag"
# use https://github.com/kyoh86/richgo to color
# note that these tests should not run verbose by default (e.g. use your IDE for this)
# TODO: add test shuffling/seeding when landed in go v1.15 (https://github.com/golang/go/issues/28592)
test:
	richgo test -cover -race -count=1 ./...

# TODO: switch to "-m direct" after go 1.17 hits: https://github.com/golang/go/issues/40364
get-go-outdated-modules: ##- (opt) Prints outdated (direct) go modules (from go.mod).
	@((go list -u -m -f '{{if and .Update (not .Indirect)}}{{.}}{{end}}' all) 2>/dev/null | grep " ") || echo "go modules are up-to-date."


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

get-module-name: ##- Prints current go module-name (pipeable).
	@echo "${GO_MODULE_NAME}"

info-module-name: ##- (opt) Prints current go module-name.
	@echo "go module-name: '${GO_MODULE_NAME}'"

set-module-name: ##- Wizard to set a new go module-name.
	@rm -f tmp/.modulename
	@$(MAKE) info-module-name
	@echo "Enter new go module-name:" \
		&& read new_module_name \
		&& echo "new go module-name: '$${new_module_name}'" \
		&& echo -n "Are you sure? [y/N]" \
		&& read ans && [ $${ans:-N} = y ] \
		&& echo -n "Please wait..." \
		&& find . -not -path '*/\.*' -not -path './Makefile' -type f -exec sed -i "s|${GO_MODULE_NAME}|$${new_module_name}|g" {} \; \
		&& echo "new go module-name: '$${new_module_name}'!"
	@rm -f tmp/.modulename

force-module-name: ##- Overwrite occurrences of 'allaboutapps.dev/aw/go-starter' with current go module-name.
	find . -not -path '*/\.*' -not -path './Makefile' -type f -exec sed -i "s|allaboutapps.dev/aw/go-starter|${GO_MODULE_NAME}|g" {} \;

get-go-ldflags: ##- (opt) Prints used -ldflags as evaluated in Makefile used in make go-build
	@echo $(LDFLAGS)

### -----------------------
# --- Make variables
### -----------------------

# only evaluated if required by a recipe
# http://make.mad-scientist.net/deferred-simple-variable-expansion/

# go module name (as in go.mod)
GO_MODULE_NAME = $(eval GO_MODULE_NAME := $$(shell \
	(mkdir -p tmp 2> /dev/null && cat tmp/.modulename 2> /dev/null) \
	|| (gsdev modulename 2> /dev/null | tee tmp/.modulename) || echo "unknown" \
))$(GO_MODULE_NAME)


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
# .ONESHELL:

# # normal POSIX bash shell mode
# SHELL = /bin/bash
# .SHELLFLAGS = -cEeuo pipefail

# # wrapped make time tracing shell, use it via MAKE_TRACE_TIME=true make <target>
# SHELL = /app/rksh
# .SHELLFLAGS = $@