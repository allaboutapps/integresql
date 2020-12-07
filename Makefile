### -----------------------
# --- Building
### -----------------------

# first is default target when running "make" without args
build: ##- Default make target: make build-pre, go-format, go-build and lint.
	@$(MAKE) go-format
	@$(MAKE) go-build
	@$(MAKE) lint

# useful to ensure that everything gets resetuped from scratch
all: ##- Runs (pretty much) all targets: make clean, init, build, info and test.
	@$(MAKE) clean
	@$(MAKE) init
	@$(MAKE) build
	@$(MAKE) info
	@$(MAKE) test

info: ##- Prints outdated and urrent go version.
	@echo "go.mod updates:"
	@$(MAKE) get-go-outdated-modules
	@echo ""
	@go version

lint: check-embedded-modules-go-not go-lint  ##- (opt) Runs golangci-lint and make check-*.

go-format: ##- (opt) Runs go format.
	go fmt ./...

go-build: ##- (opt) Runs go build.
	go build -o bin/integresql ./cmd/server

go-lint: ##- (opt) Runs golangci-lint.
	golangci-lint run --fast --timeout 5m

# https://github.com/gotestyourself/gotestsum#format 
# w/o cache https://github.com/golang/go/issues/24573 - see "go help testflag"
# note that these tests should not run verbose by default (e.g. use your IDE for this)
# TODO: add test shuffling/seeding when landed in go v1.15 (https://github.com/golang/go/issues/28592)
# tests by pkgname
test: ##- Run tests, output by package, print coverage.
	@$(MAKE) go-test-by-pkg
	@$(MAKE) go-test-print-coverage

# tests by testname
test-by-name: ##- Run tests, output by testname, print coverage.
	@$(MAKE) go-test-by-name
	@$(MAKE) go-test-print-coverage

# note that we explicitly don't want to use a -coverpkg=./... option, per pkg coverage take precedence
go-test-by-pkg: ##- (opt) Run tests, output by package.
	gotestsum --format pkgname-and-test-fails --jsonfile /tmp/test.log -- -race -cover -count=1 -coverprofile=/tmp/coverage.out ./...

go-test-by-name: ##- (opt) Run tests, output by testname.
	gotestsum --format testname --jsonfile /tmp/test.log -- -race -cover -count=1 -coverprofile=/tmp/coverage.out ./...

go-test-print-coverage: ##- (opt) Print overall test coverage (must be done after running tests).
	@printf "coverage "
	@go tool cover -func=/tmp/coverage.out | tail -n 1 | awk '{$$1=$$1;print}'

go-test-print-slowest: ##- (opt) Print slowest running tests (must be done after running tests).
	gotestsum tool slowest --jsonfile /tmp/test.log --threshold 2s

get-go-outdated-modules: ##- (opt) Prints outdated (direct) go modules (from go.mod). 
	@((go list -u -m -f '{{if and .Update (not .Indirect)}}{{.}}{{end}}' all) 2>/dev/null | grep " ") || echo "go modules are up-to-date."

watch-tests: ##- Watches *.go files and runs package tests on modifications.
	gotestsum --format testname --watch -- -race -count=1

### -----------------------
# --- Initializing
### -----------------------

init: ##-  Runs make modules, tools and tidy.
	@$(MAKE) modules
	@$(MAKE) tools
	@$(MAKE) tidy
	@go version

# cache go modules (locally into .pkg)
modules: ##- (opt) Cache packages as specified in go.mod.
	go mod download

# https://marcofranssen.nl/manage-go-tools-via-go-modules/
tools: ##- (opt) Install packages as specified in tools.go.
	cat tools.go | grep _ | awk -F'"' '{print $$2}' | xargs -tI % go install %

tidy: ##- (opt) Tidy our go.sum file.
	go mod tidy

### -----------------------
# --- SQL
### -----------------------

sql-reset: ##- Wizard to drop and create our development database.
	@echo "DROP & CREATE database:"
	@echo "  PGHOST=${PGHOST} PGDATABASE=${PGDATABASE}" PGUSER=${PGUSER}
	@echo -n "Are you sure? [y/N] " && read ans && [ $${ans:-N} = y ]
	psql -d postgres -c 'DROP DATABASE IF EXISTS "${PGDATABASE}";'
	psql -d postgres -c 'CREATE DATABASE "${PGDATABASE}" WITH OWNER ${PGUSER} TEMPLATE "template0";'

sql-drop-all: ##- Wizard to drop ALL databases: spec, development and tracked by integresql.
	@echo "DROP ALL:"
	TO_DROP=$$(psql -qtz0 -d postgres -c "SELECT 'DROP DATABASE \"' || datname || '\";' FROM pg_database WHERE datistemplate = FALSE AND datname != 'postgres';")
	@echo "$$TO_DROP"
	@echo -n "Are you sure? [y/N] " && read ans && [ $${ans:-N} = y ]
	@echo "Drop databases..."
	echo $$TO_DROP | psql -tz0 -d postgres
	@echo "Done."

# This step is only required to be executed when the "migrations" folder has changed!
sql: ##- Runs sql format, all sql related checks and finally generates internal/models/*.go.
	@$(MAKE) sql-format

sql-format: ##- (opt) Formats all *.sql files.
	@echo "make sql-format"
	@find ${PWD} -name ".*" -prune -o -type f -iname "*.sql" -print \
		| xargs -i pg_format {} -o {}

# ### -----------------------
# # --- Swagger
# ### -----------------------

# swagger: ##- (opt) Runs make swagger-concat and swagger-server.
# 	@$(MAKE) swagger-concat
# 	@$(MAKE) swagger-server

# # https://goswagger.io/usage/mixin.html
# # https://goswagger.io/usage/flatten.html
# swagger-concat: ##- (opt) Regenerates api/swagger.yml based on api/paths/*.
# 	@echo "make swagger-concat"
# 	@rm -rf api/tmp
# 	@mkdir -p api/tmp
# 	@swagger mixin \
# 		--output=api/tmp/tmp.yml \
# 		--format=yaml \
# 		--keep-spec-order \
# 		api/main.yml api/paths/* \
# 		-q
# 	@swagger flatten api/tmp/tmp.yml \
# 		--output=api/swagger.yml \
# 		--format=yaml \
# 		-q
# 	@sed -i '1s@^@# // Code generated by "make swagger"; DO NOT EDIT.\n@' api/swagger.yml

# # https://goswagger.io/generate/server.html
# # Note that we first flag all files to delete (as previously generated), regenerate, then delete all still flagged files
# # This allows us to ensure that any filewatchers (VScode) don't panic as these files are removed.
# swagger-server: ##- (opt) Regenerates internal/types based on api/swagger.yml.
# 	@echo "make swagger-server"
# 	@grep -R -L '^// Code generated .* DO NOT EDIT\.$$$$' ./internal/types \
# 		| xargs sed -i '1s#^#// DELETE ME; DO NOT EDIT.\n#'
# 	@swagger generate server \
# 		--allow-template-override \
# 		--template-dir=api/templates \
# 		--spec=api/swagger.yml \
# 		--server-package=internal/types \
# 		--model-package=internal/types \
# 		--exclude-main \
# 		--config-file=api/config/go-swagger-config.yml \
# 		--keep-spec-order \
# 		-q
# 	@find internal/types -type f -exec grep -q '^// DELETE ME; DO NOT EDIT\.$$' {} \; -delete

# watch-swagger: ##- Watches *.yml|yaml|gotmpl files in /api and runs 'make swagger' on modifications.
# 	@echo "Watching /api/**/*.yml|yaml|gotmpl. Use Ctrl-c to to stop a run or exit."
# 	watchexec -p -w api -i tmp -i api/swagger.yml --exts yml,yaml,gotmpl $(MAKE) swagger

### -----------------------
# --- Binary checks
### -----------------------

get-licenses: ##- (opt) Prints licenses of embedded modules in the compiled bin/integresql.
ifndef GITHUB_TOKEN
	$(warning Please specify GITHUB_TOKEN otherwise you will run into rate-limits!)
	$(warning https://github.com/mitchellh/golicense#github-authentication)
endif
	golicense bin/integresql || exit 0

get-embedded-modules: ##- (opt) Prints embedded modules in the compiled bin/integresql.
	go version -m -v bin/integresql

get-embedded-modules-count: ##- (opt) Prints count of embedded modules in the compiled bin/integresql.
	go version -m -v bin/integresql | grep $$'\tdep' | wc -l

check-embedded-modules-go-not: ##- (opt) Checks embedded modules in compiled bin/integresql against go.not, throws on occurance.
	@echo "make check-embedded-modules-go-not"
	@(mkdir -p tmp 2> /dev/null && go version -m -v bin/integresql > tmp/.modules)
	grep -f go.not -F tmp/.modules && (echo "go.not: Found disallowed embedded module(s) in bin/integresql!" && exit 1) || exit 0

### -----------------------
# --- Helpers
### -----------------------

clean: ##- (opt) Cleans tmp and api/tmp folder.
	rm -rf tmp
	rm -rf api/tmp

# https://gist.github.com/prwhite/8168133 - based on comment from @m000
help: ##- Show this help.
	@echo "usage: make <target>"
	@echo "note: targets flagged with '(opt)' are *internal* and executed as part of another target."
	@echo ""
	@sed -e '/#\{2\}-/!d; s/\\$$//; s/:[^#\t]*/@/; s/#\{2\}- *//' $(MAKEFILE_LIST) | sort | column -t -s '@'

### -----------------------
# --- Special targets
### -----------------------

# https://www.gnu.org/software/make/manual/html_node/Special-Targets.html
# https://www.gnu.org/software/make/manual/html_node/Phony-Targets.html
# ignore matching file/make rule combinations in working-dir
.PHONY: test help

# https://unix.stackexchange.com/questions/153763/dont-stop-makeing-if-a-command-fails-but-check-exit-status
# https://www.gnu.org/software/make/manual/html_node/One-Shell.html
# required to ensure make fails if one recipe fails (even on parallel jobs)
.ONESHELL:
SHELL = /bin/bash
.SHELLFLAGS = -ec
