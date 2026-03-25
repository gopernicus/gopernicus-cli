# gopernicus-cli

CLI for the Gopernicus Go web framework. Scaffolds projects, generates code from database schemas, and manages migrations.

## Build

```bash
go build -o gopernicus .
```

## Commands

| Command | Description |
|---------|-------------|
| `gopernicus init <name>` | Bootstrap a new project |
| `gopernicus generate` | Generate repositories and stores from DB schema |
| `gopernicus new adapter` | Scaffold a new infrastructure adapter |
| `gopernicus db migrate` | Run pending migrations |
| `gopernicus db create <name>` | Create a new migration file |
| `gopernicus db status` | Show migration status |
| `gopernicus db reflect` | Introspect database schema |
| `gopernicus doctor` | Check project health |
| `gopernicus version` | Print CLI version |

### init

```bash
gopernicus init myapp                                # interactive
gopernicus init myapp --module github.com/me/myapp   # set module path
```

Bootstraps a new Go project with the gopernicus framework as a dependency.

### generate

```bash
gopernicus generate          # generate repos + stores from schema definitions
```

Reads model definitions from `workshop/models/` and generates repository interfaces, Postgres stores, and compliance tests.

### new adapter

```bash
gopernicus new adapter cache myredis    # scaffold a new cache adapter
```

Generates adapter boilerplate with compile-time interface checks and compliance test wiring.

### db

```bash
gopernicus db migrate                   # apply pending migrations
gopernicus db create add_users_table    # create a new timestamped migration
gopernicus db status                    # show which migrations have run
gopernicus db reflect                   # introspect DB schema into model definitions
```

### doctor

```bash
gopernicus doctor       # verify framework dependency, Go version, project structure
```

## Dev Mode

Point to your local gopernicus checkout to test framework changes without pushing:

```bash
export GOPERNICUS_DEV_SOURCE=/path/to/gopernicus
```
