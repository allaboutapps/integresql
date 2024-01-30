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

## Once per test runner/process:

```mermaid
sequenceDiagram
    You->>Testrunner: make test

    Note right of Testrunner: Compute a migrations/fixtures files hash over all related database files


    Testrunner->>IntegreSQL: InitializeTemplate: POST /api/v1/templates

    Note over Testrunner,IntegreSQL: Create a new PostgreSQL template database<br/> identified a the same unique hash <br/>payload: {"hash": "string"} 

    IntegreSQL->>PostgreSQL: CREATE DATABASE <br/>template_<hash>
    PostgreSQL-->>IntegreSQL: 

    IntegreSQL-->>Testrunner: StatusOK: 200

    Note over Testrunner,PostgreSQL: Parse the received database connection payload and connect to the template database.

    Testrunner->>PostgreSQL: Truncate, apply all migrations, seed all fixtures, ..., disconnect.
    PostgreSQL-->>Testrunner: 

    Note over Testrunner,IntegreSQL: Finalize the template so it can be used!

    Testrunner->>IntegreSQL: FinalizeTemplate: PUT /api/v1/templates/:hash
    IntegreSQL-->>Testrunner: StatusOK: 200

    Note over Testrunner,IntegreSQL: You can now get isolated test databases from the pool!

    Note over Testrunner,IntegreSQL: In case you have multiple testrunners/processes <br/>and call with the same template hash again

    Testrunner->>IntegreSQL: InitializeTemplate: POST /api/v1/templates
    IntegreSQL-->>Testrunner: StatusLocked: 423

    Note over Testrunner,IntegreSQL: Some other process has already recreated <br/> a PostgreSQL template database for this hash<br/> (or is currently doing it), you can just consider<br/> the template ready at this point.

    IntegreSQL-->>Testrunner: StatusServiceUnavailable: 503

    Note over Testrunner,IntegreSQL: Typically happens if IntegreSQL cannot communicate with<br/>PostgreSQL, fail the test runner process
```
