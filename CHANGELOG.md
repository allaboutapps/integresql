# Changelog

- [Changelog](#changelog)
  - [Structure](#structure)
  - [Unreleased](#unreleased)
  - [v1.1.0](#v110)
    - [General](#general)
    - [Known issues](#known-issues)
    - [Added](#added)
    - [Changed](#changed)
    - [Environment Variables](#environment-variables)
      - [Manager/Pool-related](#managerpool-related)
      - [Server-related](#server-related)
  - [v1.0.0](#v100)


## Structure

- All notable changes to this project will be documented in this file.
- The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
- We try to follow [semantic versioning](https://semver.org/).
- All changes have a **git tag** available, are build and published to GitHub packages as a docker image.

## Unreleased

## v1.1.0

> Special thanks to [Anna - @anjankow](https://github.com/anjankow) for her contributions to this release!

### General
- Major refactor of the pool manager, while the API should still be backwards-compatible. There should not be any breaking changes when it comes to using the client libraries.
- The main goal of this release is to bring IntegreSQL's performance on par with our previous native Node.js implementation.
  - Specifially we wanted to eliminate some long-running mutex locks (especially when the pool hits the configured pool limit) and make the codebase more maintainable and easier to extend in the future.
  - While the above should be already visible in CI-environment, the subjective performance gain while developing locally could be even bigger.

### Known issues
- We still have no mechanism to limit the global (cross-pool) number of test-databases. 
  - This is especially problematic if you have many pools running at the same time. 
  - This could lead to situations where the pool manager is unable to create a new test-databases because the limit (e.g. disk size) is reached even tough some pools/test-databases will probably never be used again.
  - This is a **known issue** and will be addressed in a future release.
- OpenAPI/Swagger API documentation is still missing, we are working on it.

### Added
- GitHub Packages
  - Going forward, images are built via GitHub Actions and published to GitHub packages.
- ARM Docker images
  - Arm64 is now supported (Apple Silicon M1/M2/M3), we publish a multi-arch image (`linux/amd64,linux/arm64`).
  - Closes [#15](https://github.com/allaboutapps/integresql/issues/15)
- We added the `POST /api/v1/templates/:hash/tests/:id/recreate` endpoint to the API.
  - You can use it to express that you no longer using this database and it can be recreated and returned to the pool.
  - Using this endpoint means you want to break out of our FIFO (first in, first out) recreating queue and get your test-database recreated as soon as possible.
  - Explicitly calling recreate is **optional** of course!
  - Closes [#2](https://github.com/allaboutapps/integresql/issues/2)
- Minor: Added woodpecker/drone setup (internal allaboutapps CI/CD)

### Changed
- Redesigned Database Pool Logic and Template Management
  - Reimplemented pool and template logic, separated DB template management from test DB pool, and added per pool workers for preparing test DBs in the background.
- Soft-deprecated the `DELETE /api/v1/templates/:hash/tests/:id` endpoint in favor of `POST /api/v1/templates/:hash/tests/:id/unlock`.
  - We did a bad job describing the previous functionality of this endpoint: It's really only deleting the lock, so the exact same test-database can be used again.
  - The new `POST /api/v1/templates/:hash/tests/:id/recreate` vs. `POST /api/v1/templates/:hash/tests/:id/unlock` endpoint naming is way more explicit in what it does.
  - Closes [#13](https://github.com/allaboutapps/integresql/issues/13)
- Logging and Debugging Improvements
  - Introduced zerolog for better logging in the pool and manager modules. Debug statements were refined, and unnecessary print debugging was disabled.
- Changed details around installing locally in README.md (still not recommended, use the Docker image instead), closes [#7](https://github.com/allaboutapps/integresql/issues/7)

### Environment Variables

There have been quite a few additions and changes, thus we have the in-depth details here.

#### Manager/Pool-related

- Changed `INTEGRESQL_TEST_MAX_POOL_SIZE`:
  - Maximal pool size that won't be exceeded
  - Defaults to "your number of CPU cores 4 times" [`runtime.NumCPU()*4`](https://pkg.go.dev/runtime#NumCPU)
  - Previous default was `500` (hardcoded)
  - This might be a **significant change** for some usecases, please adjust accordingly. The pooling recreation logic is now much faster, there is typically no need to have such a high limit of test-databases **per pool**!
- Changed `INTEGRESQL_TEST_INITIAL_POOL_SIZE`:
  - Initial number of ready DBs prepared in background. The pool is configured to always try to have this number of ready DBs available (it actively tries to recreate databases within the pool in a FIFO manner).
  - Defaults to [`runtime.NumCPU()`](https://pkg.go.dev/runtime#NumCPU)
  - Previous default was `10` (hardcoded)
- Added `INTEGRESQL_POOL_MAX_PARALLEL_TASKS`:
  - Maximal number of pool tasks running in parallel. Must be a number greater or equal 1.
  - Defaults to [`runtime.NumCPU()`](https://pkg.go.dev/runtime#NumCPU)
- Added `INTEGRESQL_TEST_DB_RETRY_RECREATE_SLEEP_MIN_MS`:
  - Minimal time to wait after a test db recreate has failed (e.g. as client is still connected). Subsequent retries multiply this values until the maximum (below) is reached.
  - Defaults to `250`ms
- Added `INTEGRESQL_TEST_DB_RETRY_RECREATE_SLEEP_MAX_MS`:
  - The maximum possible sleep time between recreation retries (e.g. 3 seconds), see above.
  - Defaults to `3000`ms
- Added `INTEGRESQL_TEST_DB_MINIMAL_LIFETIME_MS`:
  - After a test-database transitions from `ready` to `dirty`, always block auto-recreation (FIFO) for this duration (expect `POST /api/v1/templates/:hash/tests/:id/recreate` was called manually).
  - Defaults to `250`ms
- Added `INTEGRESQL_TEMPLATE_FINALIZE_TIMEOUT_MS`:
  - Internal time to wait for a template-database to transition into the 'finalized' state
  - Defaults to `60000`ms (1 minute, same as `INTEGRESQL_ECHO_REQUEST_TIMEOUT_MS`)
- Added `INTEGRESQL_TEST_DB_GET_TIMEOUT_MS`:
  - Internal time to wait for a ready database (requested via `/api/v1/templates/:hash/tests`)
  - Defaults to `60000`ms (1 minute, same as `INTEGRESQL_ECHO_REQUEST_TIMEOUT_MS`)
  - Previous default `10` (was hardcoded)

#### Server-related

- Added `INTEGRESQL_DEBUG_ENDPOINTS`
  - Enables [pprof debug endpoints](https://golang.org/pkg/net/http/pprof/) under `/debug/*`
  - Defaults to `false`
- Added `INTEGRESQL_ECHO_DEBUG`
  - Enables [echo framework debug mode](https://echo.labstack.com/docs/customization)
  - Defaults to `false`
- Added middlewares, all default to `true`
  - `INTEGRESQL_ECHO_ENABLE_CORS_MIDDLEWARE`: [enables CORS](https://echo.labstack.com/docs/middleware/cors)
  - `INTEGRESQL_ECHO_ENABLE_LOGGER_MIDDLEWARE`: [enables logger](https://echo.labstack.com/docs/middleware/logger)
  - `INTEGRESQL_ECHO_ENABLE_RECOVER_MIDDLEWARE`: [enables recover](https://echo.labstack.com/docs/middleware/recover)
  - `INTEGRESQL_ECHO_ENABLE_REQUEST_ID_MIDDLEWARE`: [sets request_id to context](https://echo.labstack.com/docs/middleware/request-id)
  - `INTEGRESQL_ECHO_ENABLE_TRAILING_SLASH_MIDDLEWARE`: [auto-adds trailing slash](https://echo.labstack.com/docs/middleware/trailing-slash)
  - `INTEGRESQL_ECHO_ENABLE_REQUEST_TIMEOUT_MIDDLEWARE`: [enables timeout middleware](https://echo.labstack.com/docs/middleware/timeout)
- Added `INTEGRESQL_ECHO_REQUEST_TIMEOUT_MS`
  - Generic timeout handling for most endpoints.
  - Defaults to `60000`ms (1 minute, same as `INTEGRESQL_TEMPLATE_FINALIZE_TIMEOUT_MS` and `INTEGRESQL_TEST_DB_GET_TIMEOUT_MS`)
- Added `INTEGRESQL_LOGGER_LEVEL`
  - Defaults to `info`
- Added `INTEGRESQL_LOGGER_REQUEST_LEVEL`
  - Defaults to `info`
- Added the following logging settings, which all default to `false`
  - `INTEGRESQL_LOGGER_LOG_REQUEST_BODY`: Should the request-log include the body?
  - `INTEGRESQL_LOGGER_LOG_REQUEST_HEADER`: Should the request-log include headers?
  - `INTEGRESQL_LOGGER_LOG_REQUEST_QUERY`: Should the request-log include the query?
  - `INTEGRESQL_LOGGER_LOG_RESPONSE_BODY`: Should the request-log include the response body?
  - `INTEGRESQL_LOGGER_LOG_RESPONSE_HEADER`: Should the request-log include the response header?
  - `INTEGRESQL_LOGGER_PRETTY_PRINT_CONSOLE`: Should the console logger pretty-print the log (instead of json)?

## v1.0.0
- Initial release May 2020
