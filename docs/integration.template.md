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

# Integrate via REST API

First start IntegreSQL and leave it running in the background (your PostgreSQL template and test database pool will then always be warm). 

You then trigger your test command (e.g. `make test`). 1..n test runners/processes then start in parallel.

## Once per test runner/process:

Each test runner that starts need to communicate with IntegreSQL to set 1..n template database pools. The following sections describe the flows/scenarios you need to implement.

### Testrunner creates a new template database

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

### Testrunner reuses an existing template database

```mermaid
sequenceDiagram

    You->>Testrunner: make test

    Note over Testrunner,IntegreSQL: Subsequent testrunners or multiple processes <br/> simply call with the same template hash again.

    Testrunner->>IntegreSQL: InitializeTemplate: POST /api/v1/templates
    IntegreSQL-->>Testrunner: StatusLocked: 423

    Note over Testrunner,IntegreSQL: Some other testrunner / process has already recreated <br/> this PostgreSQL template database identified by this hash<br/> (or is currently doing it), you can just consider<br/> the template ready at this point.

    Note over Testrunner,PostgreSQL: You can now get isolated test databases for this hash from the pool! 

```

### Testrunner errors while template database setup

```mermaid
sequenceDiagram

    You->>Testrunner: make test

    Testrunner->>IntegreSQL: InitializeTemplate: POST /api/v1/templates
    IntegreSQL-->>Testrunner: StatusServiceUnavailable: 503

    Note over Testrunner,PostgreSQL: Typically happens if IntegreSQL cannot communicate with<br/>PostgreSQL, fail the test runner process in this case (e.g. exit 1).

```