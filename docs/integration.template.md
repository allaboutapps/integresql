<!-- 
This file contains [mermaid](https://mermaid.js.org) diagrams.

In VSCode:
* install `bierner.markdown-mermaid` to have easy preview.
* install `bpruitt-goddard.mermaid-markdown-syntax-highlighting` for syntax highlighting.

To Export:
* npm install -g @mermaid-js/mermaid-cli
* mmdc -i integration.template.md -o integration.md

Syntax, see https://mermaid.js.org/syntax/entityRelationshipDiagram.html
-->

### Integrate via REST API

- [Integrate via REST API](#integrate-via-rest-api)
      - [Once per test runner/process](#once-per-test-runnerprocess)
        - [Testrunner creates a new template database](#testrunner-creates-a-new-template-database)
        - [Testrunner reuses an existing template database](#testrunner-reuses-an-existing-template-database)
        - [Failure modes while template database setup: 503](#failure-modes-while-template-database-setup-503)
      - [Per each test](#per-each-test)
        - [New test database per test](#new-test-database-per-test)
        - [Optional: Manually unlocking a test database after a readonly test](#optional-manually-unlocking-a-test-database-after-a-readonly-test)
        - [Optional: Manually recreating a test database](#optional-manually-recreating-a-test-database)
        - [Failure modes while getting a new test database](#failure-modes-while-getting-a-new-test-database)
          - [StatusNotFound 404](#statusnotfound-404)
          - [StatusGone 410](#statusgone-410)
          - [StatusServiceUnavailable 503](#statusserviceunavailable-503)

First start IntegreSQL and leave it running in the background (your PostgreSQL template and test database pool will then always be warm). When you trigger your test command (e.g. `make test`), 1..n test runners/processes start in parallel.

A really good starting point to write your own integresql-client for a specific language can be found [here (go code)](https://github.com/allaboutapps/integresql-client-go/blob/master/client.go) and [here (godoc)](https://pkg.go.dev/github.com/allaboutapps/integresql-client-go?tab=doc). It's just RESTful JSON after all.

#### Once per test runner/process

Each test runner starts and need to communicate with IntegreSQL to setup 1..n template database pools. The following sections describe the flows/scenarios you need to implement.

##### Testrunner creates a new template database

```mermaid
sequenceDiagram
    You->>Testrunner: make test

    Note right of Testrunner: Compute a hash over all related <br/> files that affect your database<br/> (migrations, fixtures, imports, etc.)

    Note over Testrunner,IntegreSQL: Create a new PostgreSQL template database<br/> identified a the same unique hash <br/>payload: {"hash": "string"} 

    Testrunner->>IntegreSQL: InitializeTemplate: POST /api/v1/templates

    IntegreSQL->>PostgreSQL: CREATE DATABASE <br/>template_<hash>
    PostgreSQL-->>IntegreSQL: 

    IntegreSQL-->>Testrunner: StatusOK: 200

    Note over Testrunner,PostgreSQL: Parse the received database connection payload and connect to the template database.

    Testrunner->>PostgreSQL: Truncate, apply all migrations, seed all fixtures, ..., disconnect.
    PostgreSQL-->>Testrunner: 

    Note over Testrunner,IntegreSQL: Finalize the template so it can be used!

    Testrunner->>IntegreSQL: FinalizeTemplate: PUT /api/v1/templates/:hash
    IntegreSQL-->>Testrunner: StatusOK: 200

    Note over Testrunner,PostgreSQL: You can now get isolated test databases for this hash from the pool! 
```

##### Testrunner reuses an existing template database

```mermaid
sequenceDiagram

    You->>Testrunner: make test

    Note over Testrunner,IntegreSQL: Subsequent testrunners or multiple processes <br/> simply call with the same template hash again.

    Testrunner->>IntegreSQL: InitializeTemplate: POST /api/v1/templates
    IntegreSQL-->>Testrunner: StatusLocked: 423

    Note over Testrunner,IntegreSQL: Some other testrunner / process has already recreated <br/> this PostgreSQL template database identified by this hash<br/> (or is currently doing it), you can just consider<br/> the template ready at this point.

    Note over Testrunner,PostgreSQL: You can now get isolated test databases for this hash from the pool! 

```

##### Failure modes while template database setup: 503

```mermaid
sequenceDiagram

    You->>Testrunner: make test

    Testrunner->>IntegreSQL: InitializeTemplate: POST /api/v1/templates
    IntegreSQL-->>Testrunner: StatusServiceUnavailable: 503

    Note over Testrunner,PostgreSQL: Typically happens if IntegreSQL cannot communicate with<br/>PostgreSQL, fail the test runner process in this case (e.g. exit 1).

```

#### Per each test

##### New test database per test

Well, this is the normal flow to get a new isolated test database (prepopulated as its created from the template) for your test.

```mermaid
sequenceDiagram

    Note right of You: ...

    Note right of Testrunner: Before each test, get a new isolated test database<br/> from the pool for the template hash.

    Testrunner->>IntegreSQL: GetTestDatabase: GET /api/v1/templates/:hash/tests

    Note over Testrunner,IntegreSQL: Blocks until the template is finalized

    Note right of IntegreSQL: The test databases for the template pool<br/>were already created and are simply returned.

    IntegreSQL-->>Testrunner: StatusOK: 200

    Note over Testrunner,PostgreSQL: Your runner now has a fully isolated PostgreSQL database<br/>from our already migrated/seeded template database to use within your test.

    Testrunner->>PostgreSQL: Directly connect to the test database.

    Note over Testrunner,PostgreSQL: Run your test code!

    Testrunner-xPostgreSQL: Disconnect from the test database

     Note over Testrunner,PostgreSQL: Your test is finished.
```

##### Optional: Manually unlocking a test database after a readonly test

* Returns the given test DB directly to the pool, without cleaning (recreating it).
* **This is optional!** If you don't call this endpoints, the test database will be recreated in a FIFO manner (first in, first out) as soon as possible, even though it actually had no changes.
* This is useful if you are sure, you did not do any changes to the database and thus want to skip the recreation process by returning it to the pool directly.


```mermaid
sequenceDiagram

    Note right of You: ...

    Testrunner->>IntegreSQL: GetTestDatabase: GET /api/v1/templates/:hash/tests
    IntegreSQL-->>Testrunner: StatusOK: 200

    Testrunner->>PostgreSQL: Directly connect to the test database.

    Note over Testrunner,PostgreSQL: Run your **readonly** test code!

    Testrunner-xPostgreSQL: Disconnect from the test database

    Note over Testrunner,PostgreSQL: Your **readonly** test is finished.<br/> As you did not modify the test database, you can unlock it again<br/>(immediately available in the pool again).

    Testrunner->>IntegreSQL: ReturnTestDatabase: POST /api/v1/templates/:hash/tests/:id/unlock<br/>(previously and soft-deprecated DELETE /api/v1/templates/:hash/tests/:id) 
    IntegreSQL-->>Testrunner: StatusOK: 200
```

##### Optional: Manually recreating a test database

* Recreates the test DB according to the template and returns it back to the pool.
* **This is optional!** If you don't call this endpoint, the test database will be recreated in a FIFO manner (first in, first out) as soon as possible.
* This is useful if you have parallel testing with a mix of very long and super short tests. Our auto–FIFO recreation handling might block there.

```mermaid
sequenceDiagram

    Note right of You: ...

    Testrunner->>IntegreSQL: GetTestDatabase: GET /api/v1/templates/:hash/tests
    IntegreSQL-->>Testrunner: StatusOK: 200

    Testrunner->>PostgreSQL: Directly connect to the test database.

    Note over Testrunner,PostgreSQL: Run your test code!

    Testrunner-xPostgreSQL: Disconnect from the test database

    Note over Testrunner,PostgreSQL: Your test is finished.<br/> As you don't want to wait for FIFO autocleaning,<br/> you can manually recreate the test database.

    Testrunner->>IntegreSQL: RecreateTestDatabase: POST /api/v1/templates/:hash/tests/:id/recreate
    IntegreSQL-->>Testrunner: StatusOK: 200
```


##### Failure modes while getting a new test database

Some typical status codes you might encounter while getting a new test database.

###### StatusNotFound 404

Well, seems like someone forgot to call InitializeTemplate or it errored out.

###### StatusGone 410

There was an error during test setup with our fixtures, someone called `DiscardTemplate`, thus this template cannot be used.

###### StatusServiceUnavailable 503

Well, typically a PostgreSQL connectivity problem