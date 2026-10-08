[![Build](https://github.com/botforge-pro/tory/actions/workflows/go.yml/badge.svg)](https://github.com/botforge-pro/tory/actions/workflows/go.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/botforge-pro/tory/v2.svg)](https://pkg.go.dev/github.com/botforge-pro/tory/v2)
## Tory

SQL lives in `.sql` files, Go calls it by name. A thin layer over
[pgx](https://github.com/jackc/pgx) for PostgreSQL: named queries, named arguments, rows scanned
into your own types, transactions that nest. Inspired by [dotsql](https://github.com/qustavo/dotsql).

### Install

```
go get github.com/botforge-pro/tory/v2
```

```go
import "github.com/botforge-pro/tory/v2"
```

The API is built on methods that take type parameters, so it needs Go 1.27 or newer.

### Queries

**queries.sql**

```sql
-- name: get-user-by-id
SELECT id, name FROM users WHERE id = :id;

-- name: list-users-by-city
SELECT id, name FROM users WHERE city = :city ORDER BY name;
```

**main.go**

```go
//go:embed *.sql
var sqlFiles embed.FS

type User struct {
	ID   int
	Name string
}

func main() {
	pool, err := pgxpool.New(context.Background(), "postgres://...")
	if err != nil {
		panic(err)
	}

	t := tory.New(pool)
	if err := t.Load(sqlFiles); err != nil {
		panic(err)
	}

	ctx := context.Background()

	user, err := t.Get[User](ctx, "get-user-by-id", tory.Args{"id": 42})
	if err != nil {
		panic(err)
	}
	if user == nil {
		log.Println("no such user")
	}

	users, err := t.Select[User](ctx, "list-users-by-city", tory.Args{"city": "Belgrade"})
	if err != nil {
		panic(err)
	}
	log.Println("users:", users)
}
```

One name carries one statement, and `Load` refuses a block that holds a second. PostgreSQL will run
several in a body that binds nothing and refuse the same body once it takes an argument, so a block
of two works only until somebody adds a `:name` to it. A `do $$ … end $$;` body is one statement
however many it holds inside.

No rows is not one answer here. `Get` returns `(nil, nil)`, so a row that does not exist is read
from the pointer and never from the error: `errors.Is(err, pgx.ErrNoRows)` after it is a branch that
cannot run, and code that leans on it walks into the nil instead. `Select` gives an empty slice.
`Scalar` and `QueryRow` do report it as an error, having nothing to hand back otherwise.

### Transactions

Transactions are a method, and they nest: PostgreSQL runs a nested one as a savepoint, so a batch
the server rejects costs that batch rather than the whole import:

```go
var skipped []Batch

report, err := t.Atomic(ctx, func(tx tory.Tx) (*Report, error) {
	for _, batch := range batches {
		_, err := tx.Atomic(ctx, func(tx tory.Tx) (any, error) {
			return nil, tx.Exec(ctx, "import-batch", tory.Args{"rows": batch.Rows})
		})
		if tory.IsViolationOfCheckConstraint(err) {
			skipped = append(skipped, batch) // the outer transaction is still alive
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return tx.Get[Report](ctx, "build-report", nil)
})
```

### Migrations

Migrations here are not a stack replayed from zero. The current shape is yours to declare and
yours to apply: `create table ... if not exists` that your own start-up runs on every boot.
`ApplyPatches` never touches those. It carries a database created against an older shape up to
the current one, and remembers where it got to.

```go
//go:embed *.sql
var sqlFiles embed.FS

if err := t.Load(sqlFiles); err != nil {
	panic(err)
}

version, err := tory.ApplyPatches(ctx, t, tory.ApplyPatchesOptions{
	Prefix:  "myapp-patches.",
	OnStart: func(p tory.Patch) { log.Println("applying", p.Name) },
})
if err != nil {
	panic(err)
}
log.Println("schema at version", version.Version)
```

Patches are ordinary named queries, loaded like any other and picked out by `Prefix`. Everything
under that prefix has to parse as a patch, so give patches a prefix of their own. `Load` reads the
root of the embed and no deeper, so every `.sql` file it should see sits flat in it; a separate
`patches.sql` beside the query files is the usual shape.

A patch is identified by the number its name carries after the prefix, so
`myapp-patches.0007-add-language` is version 7. The number is the only part recorded, which leaves
the words after it free to say what the patch is for: renaming one cannot re-run it. The number
needs a hyphen after it, leading zeros are trimmed, and two names that trim to the same number stop
the run rather than let one of them go quietly unapplied.

A database goes on from the highest version recorded in `db_version` and runs only the patches
above it. A patch numbered below is skipped, which is not an error: `OnSkip` hears of it, and
nothing else does. So a patch is never edited once it has run, never slipped in between two others,
and its number is never given to another: each of those lands below a version some database already
stands at, and there it never runs. A developer's database that ran an unmerged branch stands above
the patches written after it in the same way, and skips them until it is rebuilt.

A database with no rows in `db_version`, the table `ApplyPatches` creates for itself, is baselined
to the latest version without a single body running. That is what lets a fresh install start
current, and it is the sharp edge of the mechanism twice over. A fresh database never runs a patch,
so a change to the shape goes in twice: as a patch for the databases already alive, and into the
declared schema for the ones not yet created. And a populated database that predates tory is
declared current too, with every patch unrun. Adopt tory on one of those deliberately: ship
`ApplyPatches` with a first patch that changes nothing, and put the first real change in the patch
after it, once every live database has been baselined.

Everything runs in one transaction. A patch that fails leaves the database as it was and the run
repeats from the top, so no body is ever applied twice on top of itself. What a body does have to
survive is meeting its own change already made: a table created today from your declared schema
already carries the column that patch 7 adds. Write `add column if not exists`.

### Elsewhere

Everything else — the full method set, what a savepoint does and does not hold, the error helpers —
is in the [package documentation](https://pkg.go.dev/github.com/botforge-pro/tory/v2). Changes
between releases are in [CHANGELOG.md](CHANGELOG.md).
