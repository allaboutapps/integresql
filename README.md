# IntegreSQL

`IntegreSQL` manages isolated PostgreSQL databases for your integration tests.

## Overview [![](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/allaboutapps/integresql?tab=doc) [![](https://goreportcard.com/badge/github.com/allaboutapps/integresql)](https://goreportcard.com/report/github.com/allaboutapps/integresql) ![](https://github.com/allaboutapps/integresql/workflows/build/badge.svg?branch=master)

## Table of Contents

- [Background](#background)
- [Install](#install)
    - [Install using Docker (preferred)](#install-using-docker-preferred)
    - [Install locally](#install-locally)
- [Configuration](#configuration)
- [Usage](#usage)
    - [Run using Docker (preferred)](#run-using-docker-preferred)
    - [Run locally](#run-locally)
- [Contributing](#contributing)
    - [Development setup](#development-setup)
    - [Development quickstart](#development-quickstart)
- [Maintainers](#maintainers)
- [License](#license)

## Background

## Install

### Install using Docker (preferred)

A minimal Docker image containing a pre-built `IntegreSQL` executable is available at [Docker Hub](https://hub.docker.com/r/allaboutapps/integresql).

```bash
docker pull allaboutapps/integresql
```

### Install locally

Installing `IntegreSQL` locally requires a working [Go](https://golang.org/dl/) (1.14 or above) environment. Install the `IntegreSQL` executable to your Go bin folder:

```bash
go get github.com/allaboutapps/integresql/cmd/server
```

## Configuration

`IntegreSQL` requires little configuration, all of which has to be provided via environment variables (due to the intended usage in a Docker environment). The following settings are available:

| Description                                                       | Environment variable                  | Default              | Required |
|-------------------------------------------------------------------|---------------------------------------|----------------------|----------|
| IntegreSQL: listen address (defaults to all if empty)             | `INTEGRESQL_ADDRESS`                  | `""`                 |          |
| IntegreSQL: port                                                  | `INTEGRESQL_PORT`                     | `5000`               |          |
| PostgreSQL: host                                                  | `INTEGRESQL_PGHOST`, `PGHOST`         | `"127.0.0.1"`        | Yes      |
| PostgreSQL: port                                                  | `INTEGRESQL_PGPORT`, `PGPORT`         | `5432`               |          |
| PostgreSQL: username                                              | `INTEGRESQL_PGUSER`, `PGUSER`, `USER` | `"postgres"`         | Yes      |
| PostgreSQL: password                                              | `INTEGRESQL_PGPASSWORD`, `PGPASSWORD` | `""`                 | Yes      |
| PostgreSQL: database for manager                                  | `INTEGRESQL_PGDATABASE`               | `"postgres"`         |          |
| PostgreSQL: template database to use                              | `INTEGRESQL_ROOT_TEMPLATE`            | `"template0"`        |          |
| Managed databases: prefix                                         | `INTEGRESQL_DB_PREFIX`                | `"integresql"`       |          |
| Managed *template* databases: prefix `integresql_template_<HASH>` | `INTEGRESQL_TEMPLATE_DB_PREFIX`       | `"template"`         |          |
| Managed *test* databases: prefix `integresql_test_<HASH>_<ID>`    | `INTEGRESQL_TEST_DB_PREFIX`           | `"test"`             |          |
| Managed *test* databases: username                                | `INTEGRESQL_TEST_PGUSER`              | PostgreSQL: username |          |
| Managed *test* databases: password                                | `INTEGRESQL_TEST_PGPASSWORD`          | PostgreSQL: password |          |
| Managed *test* databases: minimal test pool size                  | `INTEGRESQL_TEST_INITIAL_POOL_SIZE`   | `10`                 |          |
| Managed *test* databases: maximal test pool size                  | `INTEGRESQL_TEST_MAX_POOL_SIZE`       | `500`                |          |


## Usage

### Run using Docker (preferred)

Simply start the `IntegreSQL` [Docker](https://docs.docker.com/install/) (19.03 or above) container, provide the required environment variables and expose the server port:

```bash
docker run -d --name integresql -e INTEGRESQL_PORT=5000 -p 5000:5000 allaboutapps/integresql
```

`IntegreSQL` can also be included in your project via [Docker Compose](https://docs.docker.com/compose/install/) (1.25 or above):

```yaml
version: "3.4"
services:

  # Your main service image
  service:
    depends_on:
      - postgres
      - integresql
    environment:
      PSQL_DBNAME: &PSQL_DBNAME "test" 
      PSQL_USER: &PSQL_USER "test"
      PSQL_PASS: &PSQL_PASS "testpass"
      PSQL_HOST: &PSQL_HOST "postgres"

      # [...] additional main service setup

  integresql:
    image: allaboutapps/integresql:latest
    ports:
      - "5000:5000"
    depends_on:
      - postgres
    environment: 
      PGHOST: *PSQL_HOST
      PGUSER: *PSQL_USER
      PGPASSWORD: *PSQL_PASS

  postgres:
    image: postgres:12.2-alpine
    command: "postgres -c 'shared_buffers=128MB' -c 'fsync=off' -c 'synchronous_commit=off' -c 'full_page_writes=off' -c 'max_connections=100' -c 'client_min_messages=warning'" # not necessarily required, some performance optimization for rapid integration testing, validate parameters for own requirements
    expose:
      - "5432"
    ports:
      - "5432:5432"
    environment:
      POSTGRES_DB: *PSQL_DBNAME
      POSTGRES_USER: *PSQL_USER
      POSTGRES_PASSWORD: *PSQL_PASS
    volumes:
      - pgvolume:/var/lib/postgresql/data

volumes:
  pgvolume: # declare a named volume to persist DB data
```

### Run locally

Running the `IntegreSQL` server locally requires configuration via exported environment variables (see below):

```bash
export INTEGRESQL_PORT=5000
export PSQL_HOST=127.0.0.1
export PSQL_USER=test
export PSQL_PASS=testpass
integresql
```

## Contributing

Pull requests are welcome. For major changes, please [open an issue](https://github.com/allaboutapps/integresql/issues/new) first to discuss what you would like to change.

Please make sure to update tests as appropriate.

### Development setup

`IntegreSQL` requires the following local setup for development:

- [Docker CE](https://docs.docker.com/install/) (19.03 or above)
- [Docker Compose](https://docs.docker.com/compose/install/) (1.25 or above)

The project makes use of the [devcontainer functionality](https://code.visualstudio.com/docs/remote/containers) provided by [Visual Studio Code](https://code.visualstudio.com/) so no local installation of a Go compiler is required when using VSCode as an IDE.

Should you prefer to develop `IntegreSQL` without the Docker setup, please ensure a working [Go](https://golang.org/dl/) (1.14 or above) environment has been configured as well as a PostgreSQL instance is available (tested against version 12 or above, but *should* be compatible to lower versions) and the appropriate environment variables have been configured as described in the [Install](#install) section.

### Development quickstart

1. Start the local docker-compose setup and open an interactive shell in the development container:

```bash
# Build the development Docker container, start it and open a shell
./docker-helper.sh --up
```

2. Initialize the project, downloading all dependencies and tools required (executed within the dev container):

```bash
# Init dependencies/tools
make init

# Build executable (generate, format, build, vet)
make
```

3. Execute project tests and start server:

```bash
# Execute tests
make test

# Run IntegreSQL server with config from environment
integresql
```

## Maintainers

- [Nick Müller - @MorpheusXAUT](https://github.com/MorpheusXAUT)
- [Mario Ranftl - @majodev](https://github.com/majodev)

## License

[MIT](LICENSE) © 2020 aaa – all about apps GmbH | Nick Müller | Mario Ranftl and the `IntegreSQL` project contributors