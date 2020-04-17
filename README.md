# IntegreSQL

> Isolate your PostgreSQL databases per integration test

### Quickstart

#### Via *docker-compose*

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

      [...]

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
    image: postgres:12.2-alpine # should be the same version as used live
    command: "postgres -c 'shared_buffers=128MB' -c 'fsync=off' -c 'synchronous_commit=off' -c 'full_page_writes=off' -c 'max_connections=100' -c 'client_min_messages=warning'"
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

### Environment Variables

| Description                                                  | ENV                                    | Default                         | Required |
| ------------------------------------------------------------ | -------------------------------------- | ------------------------------- | -------- |
| IntegreSQL **Server**: Port                                  | `INTEGRESQL_PORT`                      | `5000`                          |          |
| PostgreSQL: Host                                             | ` INTEGRESQL_PGHOST` ` PGHOST`         | ` "127.0.0.1"`                  | x        |
| PostgreSQL: Port                                             | ` INTEGRESQL_PGPORT` ` PGPORT`         | ` 5432`                         |          |
| PostgreSQL: Username                                         | ` INTEGRESQL_PGUSER` ` PGUSER` ` USER` | `"postgres"`                    | x        |
| PostgreSQL: Password                                         | ` INTEGRESQL_PGPASSWORD` ` PGPASSWORD` | `""`                            | x        |
| PostgreSQL: Base database connection (typically leave the default) | ` INTEGRESQL_PGDATABASE`               | `"postgres"`                    |          |
| PostgreSQL: Template for template databases (typically leave the default) | ` INTEGRESQL_TEMPLATE0`                | `"template0"`                   |          |
| Autocreated databases: prefix                                | ` INTEGRESQL_DBPREFIX`                 | `"integresql"`                  |          |
| Autocreated *Template* databases: prefix `integresql_template_<HASH>` | ` INTEGRESQL_TEMPLATE_DBPREFIX`        | `"template"`                    |          |
| Autocreated *Test* databases: prefix `integresql_test_<HASH>_<ID>` | ` INTEGRESQL_TEST_DBPREFIX`            | `"test"`                        |          |
| Autocreated *Test* databases: Username                       | `INTEGRESQL_TEST_PGUSER`               | PostgreSQL: Username            |          |
| Autocreated *Test* databases: Password                       | ` INTEGRESQL_TEST_PGPASSWORD`          | PostgreSQL: Password            |          |
| Autocreated *Test* databases: Minimal test  pool size        | ` INTEGRESQL_TEST_INITIAL_POOL_SIZE`   | `10`                            |          |
| Autocreated *Test* databases: Maximal test pool size (afterwards DBs are reused) | ` INTEGRESQL_TEST_MAX_POOL_SIZE`       | ` 500`                          |          |
|                                                              |                                        |                                 |          |
| IntegreSQL **Client**:  BaseURL                              | ` INTEGRESQL_CLIENT_BASE_URL`          | ` "http://integresql:5000/api"` | x        |
| IntegreSQL **Client**:  APIVersion                           | ` INTEGRESQL_CLIENT_API_VERSION`       | `"v1"`                          |          |

### Development Quickstart

> Only required if you want to contribute to this repository  
> Requires docker and docker-compose installed locally

```bash

./docker-helper.sh --up

# You should now have a docker shell...

# Init install/cache dependencies and install tools to bin
make init

# Building (generate, format, build, vet)
make

# Execute tests
make test

# Run the server
integresql

```

### Contributors

* [Nick Müller -- @MorpheusXAUT](https://github.com/MorpheusXAUT)
* [Mario Ranftl -- @majodev](https://github.com/majodev)

### License

MIT License, see `LICENSE.txt`.