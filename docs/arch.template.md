<!-- 
This file contains [mermaid](https://mermaid.js.org) diagrams.

In VSCode:
* install `bierner.markdown-mermaid` to have easy preview.
* install `bpruitt-goddard.mermaid-markdown-syntax-highlighting` for syntax highlighting.

To Export:
* npm install -g @mermaid-js/mermaid-cli
* mmdc -i arch.template.md -o arch.md

Syntax, see https://mermaid.js.org/syntax/entityRelationshipDiagram.html
-->

# IntegreSQL Architecture

## TestDatabase states

The following describes the state and transitions of a TestDatabase.

```mermaid
stateDiagram-v2

    HashPool --> TestDatabase: Task EXTEND

    state TestDatabase {
        [*] --> ready: init
        ready --> dirty: GetTestDatabase()
        dirty --> ready: ReturnTestDatabase()
        dirty --> recreating: RecreateTestDatabase()\nTask CLEAN_DIRTY
        recreating --> ready: generation++
        recreating --> recreating: retry (still in use)
    }
```

## Pool structure

The following describes the relationship between the components of IntegreSQL.

```mermaid
erDiagram
    Server ||--o| Manager : owns
    Manager {
        Template[] templateCollection
        HashPool[] poolCollection
    }
    Manager ||--o{ HashPool : has
    Manager ||--o{ Template : has
    Template {
        TemplateDatabase database
    }
    HashPool {
        TestDatabase database
    }
    HashPool ||--o{ TestDatabase : "manages"
    Template ||--|| TemplateDatabase : "sets"
    TestDatabase {
        int ID
        Database database
    }
    TemplateDatabase {
        Database database
    }
    Database {
        string TemplateHash
        Config DatabaseConfig
    }
    TestDatabase o|--|| Database : "is"
    TemplateDatabase o|--|| Database : "is"
```

