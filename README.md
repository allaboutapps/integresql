# IntegreSQL

`IntegreSQL` manages isolated PostgreSQL databases for your integration tests.

Do your engineers a favour by allowing them to write fast executing, parallel and deterministic integration tests utilizing **real** PostgreSQL test databases. Resemble your live environment in tests as close as possible.   

[![](https://img.shields.io/docker/image-size/allaboutapps/integresql)](https://hub.docker.com/r/allaboutapps/integresql) [![](https://img.shields.io/docker/pulls/allaboutapps/integresql)](https://hub.docker.com/r/allaboutapps/integresql) [![Docker Cloud Build Status](https://img.shields.io/docker/cloud/build/allaboutapps/integresql)](https://hub.docker.com/r/allaboutapps/integresql) [![](https://goreportcard.com/badge/github.com/allaboutapps/integresql)](https://goreportcard.com/report/github.com/allaboutapps/integresql) ![](https://github.com/allaboutapps/integresql/workflows/build/badge.svg?branch=master)

- [IntegreSQL](#integresql)
  - [Background](#background)
    - [Approach 0: Leaking database mutations for subsequent tests](#approach-0-leaking-database-mutations-for-subsequent-tests)
    - [Approach 1: Isolating by resetting](#approach-1-isolating-by-resetting)
    - [Approach 2a: Isolation by transactions](#approach-2a-isolation-by-transactions)
    - [Approach 2b: Isolation by mocking](#approach-2b-isolation-by-mocking)
    - [Approach 3a: Isolation by templates](#approach-3a-isolation-by-templates)
    - [Approach 3b: Isolation by cached templates](#approach-3b-isolation-by-cached-templates)
    - [Approach 3c: Isolation by cached templates and pool](#approach-3c-isolation-by-cached-templates-and-pool)
      - [Approach 3c benchmark 1: Baseline](#approach-3c-benchmark-1-baseline)
      - [Approach 3c benchmark 2: Small project](#approach-3c-benchmark-2-small-project)
    - [Final approach: IntegreSQL](#final-approach-integresql)
      - [Integrate by client lib](#integrate-by-client-lib)
      - [Integrate by RESTful JSON calls](#integrate-by-restful-json-calls)
      - [Demo](#demo)
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

We came a long way to realize that something just did not feel right with our PostgreSQL integration testing strategies.
This is a loose summary of how this project came to life.

### Approach 0: Leaking database mutations for subsequent tests

Testing our customer backends actually started quite simple:

* Test runner starts
* Recreate a PostgreSQL test database
* Apply all migrations
* Seed all fixtures
* Utilizing the same PostgreSQL test database for each test:
  * **Run your test code** 
* Test runner ends

It's quite easy to spot the problem with this approach. Data may be mutated by any single test and is visible from all subsequent tests. It becomes cumbersome to make changes in your test code if you can't rely on a clean state in each and every test.

### Approach 1: Isolating by resetting

Let's try to fix that like this:

* Test runner starts
* Recreate a PostgreSQL test database
* **Before each** test: 
  * Truncate
  * Apply all migrations
  * Seed all fixtures
* Utilizing the same PostgreSQL test database for each test:
  * **Run your test code** 
* Test runner ends

Well, it's now isolated - but testing time has increased by a rather high factor and is totally dependent on your truncate/migrate/seed operations.

### Approach 2a: Isolation by transactions

What about using database transactions?

* Test runner starts
* Recreate a PostgreSQL test database
* Apply all migrations
* Seed all fixtures
* **Before each** test: 
  * Start a new database transaction
* Utilizing the same PostgreSQL test database for each test:
  * **Run your test code** 
* **After each** test:
  * Rollback the database transaction
* Test runner ends

After spending various time to rewrite all code to actually use the injected database transaction in each code, you realize that nested transactions are not supported and can only be poorly emulated using save points. All database transaction specific business code, especially their potential error state, is not properly testable this way. You therefore ditch this approach.

### Approach 2b: Isolation by mocking

What about using database mocks?

* Test runner starts
* Utilizing an in-memory mock database isolated for each test:
  * **Run your test code** 
* Test runner ends

I'm generally not a fan of emulating database behavior through a mocking layer while testing/implementing. Even minor version changes of PostgreSQL plus it's extensions (e.g. PostGIS) may introduce slight differences, e.g. how indices are used, function deprecations, query planner, etc. . It might not even be an erroneous result, just performance regressions or slight sorting differences in the returned query result.

We try to approximate local/test and live as close as possible, therefore using the same database, with the same extensions in their exact same version is a hard requirement for us while implementing/testing locally.

### Approach 3a: Isolation by templates

We discovered that using [PostgreSQL templates](https://supabase.com/blog/2020/07/09/postgresql-templates) and creating the actual new test database from them is quite fast, let's to this:

* Test runner starts
* Recreate a PostgreSQL template database
* Apply all migrations
* Seed all fixtures
* **Before each** test: 
  * Create a new PostgreSQL test database from our already migrated/seeded template database
* Utilizing a new isolated PostgreSQL test database for each test:
  * **Run your test code** 
* Test runner ends

Well, we are up in speed again, but we still can do better, how about...

### Approach 3b: Isolation by cached templates

* Test runner starts
* Check migrations/fixtures have changed (hash over all related files)
  * Yes
    * Recreate a PostgreSQL template database
    * Apply all migrations
    * Seed all fixtures
  * No, nothing has changed
    * Simply reuse the previous PostgreSQL template database
* **Before each** test: 
  * Create a new PostgreSQL test database from our already migrated/seeded template database
* Utilizing a new isolated PostgreSQL test database for each test:
  * **Run your test code** 
* Test runner ends

This gives a significant speed bump as we no longer need to recreate our template database if no files related to the database structure or fixtures have changed. However, we still need to create a new PostgreSQL test database from a template before running any test. Even though this is quite fast, could we do better?

### Approach 3c: Isolation by cached templates and pool

* Test runner starts
* Check migrations/fixtures have changed (hash over all related files)
  * Yes
    * Recreate a PostgreSQL template database
    * Apply all migrations
    * Seed all fixtures
  * No, nothing has changed
    * Simply reuse the previous PostgreSQL template database
* Create a pool of n PostgreSQL test databases from our already migrated/seeded template database
* **Before each** test: 
  * Select the first new PostgreSQL test database that is ready from the test pool
* Utilizing your selected PostgreSQL test database from the test pool for each test:
  * **Run your test code** 
* **After each** test: 
  * If there are still tests lefts to run add some additional PostgreSQL test databases from our already migrated/seeded template database
* Test runner ends

Finally, by keeping a warm pool of test database we arrive at the speed of Approach 0, while having the isolation gurantees of all subsequent approaches.
This is actually the (simplified) strategy, that we have used in [allaboutapps-backend-stack](https://github.com/allaboutapps/aaa-backend-stack) for many years.

#### Approach 3c benchmark 1: Baseline

Here's a quick benchmark of how this strategy typically performed back then:

```
--- ----------------<storageHelper strategy report>---------------- ---
    replicas switched:          50     avg=11ms min=1ms max=445ms
    replicas awaited:           1      prebuffer=8 avg=436ms max=436ms
    background replicas:        58     avg=272ms min=41ms max=474ms
    - warm up template (cold):  82%    2675ms
        * truncate:             62%    2032ms
        * migrate:              18%    594ms
        * seed:                 1%     45ms
    - switching:                17%    571ms
        * disconnect:           1%     42ms
        * switch replica:       14%    470ms
            - resolve next:     1%     34ms
            - await next:       13%    436ms
        * reinitialize:         1%     57ms
    strategy related time:      ---    3246ms
    vs total executed time:     20%    15538ms
--- ---------------</ storageHelper strategy report>--------------- ---
```

This is a rather small testsuite with `50` tests and with a tiny database. Thus the whole test run was finished in `~15sec`. `~2.7sec` were spend setting up the template within the warm up (truncate + migrate + seed) and `~0.6sec` in total waiting for a new test/replica databases to become available for a test. We spend `~20%` of our total execution time running / waiting inside our test strategy approach. 

This a cold start. You pay for this warm-up flow only if no template database was cached by a previous test run (if your migrations + fixtures files - the `hash` over these files - hasn't changed).

A new test database (called a replica here) from this tiny template database took max. `~500ms` to create, on avg. this was ~halfed and most importantly can be done in the background (while some tests already execute).

The cool thing about having a warm pool of replicas setup in the background, is that selecting new replicas from the pool is blazingly fast, as typically they *will be already ready* when it's time to execute the next test. For instance, it took `~500ms` max. and **`11ms` on avg.** to select a new replica for all subsequent tests (we only had to wait once until a replica became available for usage within a test - typically it's the first test to be executed).

#### Approach 3c benchmark 2: Small project

Let's look at a sightly bigger testsuite and see how this approach may possibly scale:

```
--- -----------------<storageHelper strategy report>------------------ ---
    replicas switched:             280    avg=26ms min=11ms max=447ms
    replicas awaited:              1      prebuffer=8 avg=417ms max=417ms
    background replicas:           288    avg=423ms min=105ms max=2574ms
    - warm up template (cold):     40%    5151ms
        * truncate:                8%     980ms
        * migrate:                 26%    3360ms
        * seed:                    4%     809ms
    - switching:                   60%    7461ms
        * disconnect:              2%     322ms
        * switch replica:          6%     775ms
            - resolve next:        2%     358ms
            - await next:          3%     417ms
        * reinitialize:            50%    6364ms
    strategy related time:         ---    12612ms
    vs total executed time:        11%    111094ms
--- ----------------</ storageHelper strategy report>----------------- ---
```

This test suite is larger and comes with `280` tests, the whole test run finished in `~1m50s` (`~390ms` per test on avg.). `~5.2sec` were spend setting up the template and `~7.5sec` in total waiting for a new test / replica databases to become available for a test.

The rise in switching time is expected, as we need way more replicas / test databases this time, however we only spend `~11%` running / waiting inside our test strategy approach. To put that into perspective, each test only had to **wait `~26ms` on avg.** until it could finally execute (and typically, this is solely the time it needs to open up a new database connection).

This should hopefully give you some base understanding on why we consider this testing approach essential for our projects. It's the sweet combination of speed and isolation. 

### Final approach: IntegreSQL

We realized that having the above pool logic directly within the test runner is actually counterproductive and is further limiting usage from properly utilizing parallel testing (+performance).

As we switched to Go as our primary backend engineering language, we needed to rewrite the above logic anyways and decided to provide a safe and language agnostic way to utilize this testing strategy with PostgreSQL.

IntegreSQL is a RESTful JSON API distributed as Docker image or go cli. It's language agnostic and manages multiple [PostgreSQL templates](https://supabase.io/blog/2020/07/09/postgresql-templates/) and their separate pool of test databases for your tests. It keeps the pool of test databases warm (as it's running in the background) and is fit for parallel test execution with multiple test runners / processes.

Our flow now finally changed to this:

* **Start IntegreSQL** and leave it running **in the background** (your PostgreSQL template and test database pool will always be warm)
* ...
* 1..n test runners start in parallel
* Once per test runner process
  * Get migrations/fixtures files `hash` over all related database files
  * `InitializeTemplate: POST /templates`: attempt to create a new PostgreSQL template database identifying though the above hash `payload: {"hash": "string"}`
    * `StatusOK: 200` 
      * Truncate
      * Apply all migrations
      * Seed all fixtures
      * `FinalizeTemplate: PUT /templates/{hash}` 
      * If you encountered any template setup errors call `DiscardTemplate: DELETE /templates/{hash}`
    * `StatusLocked: 423`
      * Some other process has already recreated a PostgreSQL template database for this `hash` (or is currently doing it), you can just consider the template ready at this point.
    * `StatusServiceUnavailable: 503`
      * Typically happens if IntegreSQL cannot communicate with PostgreSQL, fail the test runner process
* **Before each** test `GetTestDatabase: GET /templates/{hash}/tests`
  * Blocks until the template database is finalized (via `FinalizeTemplate`)
  * `StatusOK: 200`
    * You get a fully isolated PostgreSQL database from our already migrated/seeded template database to use within your test
  * `StatusNotFound: 404`
    * Well, seems like someone forgot to call `InitializeTemplate` or it errored out.
  * `StatusGone: 410`
    * There was an error during test setup with our fixtures, someone called `DiscardTemplate`, thus this template cannot be used.
  * `StatusServiceUnavailable: 503`
    * Well, typically a PostgreSQL connectivity problem
* Utilizing the isolated PostgreSQL test database received from IntegreSQL for each (parallel) test:
  * **Run your test code**
* **After each** test optional: `ReturnTestDatabase: DELETE /templates/{hash}/tests/{test-database-id}`
  * Marks the test database that it can be wiped early on pool limit overflow (or reused if `true` is submitted)
* 1..n test runners end
* ...
* Subsequent 1..n test runners start/end in parallel and reuse the above logic

#### Integrate by client lib

The flow above might look intimidating at first glance, but trust us, it's simple to integrate especially if there is already an client library available for your specific language. We currently have those:

* Go: [integresql-client-go](https://github.com/allaboutapps/integresql-client-go) by [Nick Müller - @MorpheusXAUT](https://github.com/MorpheusXAUT)
* Python: [integresql-client-python](https://github.com/msztolcman/integresql-client-python) by [Marcin Sztolcman - @msztolcman](https://github.com/msztolcman)
* ... *Add your link here and make a PR*

#### Integrate by RESTful JSON calls

A really good starting point to write your own integresql-client for a specific language can be found [here (go code)](https://github.com/allaboutapps/integresql-client-go/blob/master/client.go) and [here (godoc)](https://pkg.go.dev/github.com/allaboutapps/integresql-client-go?tab=doc). It's just RESTful JSON after all.

#### Demo

If you want to take a look on how we integrate IntegreSQL - 🤭 - please just try our [go-starter](https://github.com/allaboutapps/go-starter) project or take a look at our [testing setup code](https://github.com/allaboutapps/go-starter/blob/master/internal/test/testing.go). 

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
| ----------------------------------------------------------------- | ------------------------------------- | -------------------- | -------- |
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
      PGDATABASE: &PGDATABASE "development"
      PGUSER: &PGUSER "dbuser"
      PGPASSWORD: &PGPASSWORD "9bed16f749d74a3c8bfbced18a7647f5"
      PGHOST: &PGHOST "postgres"
      PGPORT: &PGPORT "5432"
      PGSSLMODE: &PGSSLMODE "disable"

      # optional: env for integresql client testing
      # see https://github.com/allaboutapps/integresql-client-go
      # INTEGRESQL_CLIENT_BASE_URL: "http://integresql:5000/api"

      # [...] additional main service setup

  integresql:
    image: allaboutapps/integresql:1.0.0
    ports:
      - "5000:5000"
    depends_on:
      - postgres
    environment: 
      PGHOST: *PGHOST
      PGUSER: *PGUSER
      PGPASSWORD: *PGPASSWORD

  postgres:
    image: postgres:12.2-alpine # should be the same version as used live
    # ATTENTION
    # fsync=off, synchronous_commit=off and full_page_writes=off
    # gives us a major speed up during local development and testing (~30%),
    # however you should NEVER use these settings in PRODUCTION unless
    # you want to have CORRUPTED data.
    # DO NOT COPY/PASTE THIS BLINDLY.
    # YOU HAVE BEEN WARNED.
    # Apply some performance improvements to pg as these guarantees are not needed while running locally
    command: "postgres -c 'shared_buffers=128MB' -c 'fsync=off' -c 'synchronous_commit=off' -c 'full_page_writes=off' -c 'max_connections=100' -c 'client_min_messages=warning'"
    expose:
      - "5432"
    ports:
      - "5432:5432"
    environment:
      POSTGRES_DB: *PGDATABASE
      POSTGRES_USER: *PGUSER
      POSTGRES_PASSWORD: *PGPASSWORD
    volumes:
      - pgvolume:/var/lib/postgresql/data

volumes:
  pgvolume: # declare a named volume to persist DB data
```

You may also refer to our [go-starter `docker-compose.yml`](https://github.com/allaboutapps/go-starter/blob/master/docker-compose.yml).

### Run locally

Running the `IntegreSQL` server locally requires configuration via exported environment variables (see below):

```bash
export INTEGRESQL_PORT=5000
export PGHOST=127.0.0.1
export PGUSER=test
export PGPASSWORD=testpass
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
