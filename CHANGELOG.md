# Changelog

Tory is a thin PostgreSQL layer over [pgx](https://github.com/jackc/pgx): SQL lives in `.sql` files
as named queries, Go calls them by name and scans rows into its own types. The
[package documentation](https://pkg.go.dev/github.com/botforge-pro/tory/v2) carries the whole API and
the [Readme](https://github.com/botforge-pro/tory#readme) introduces it; this file records what
changed between releases.

Two names run through these entries. `Load` reads the `.sql` files at start-up and is what accepts
or refuses them, so a release that changes what it accepts stops the program before anything else
happens. `ApplyPatches` is the migration side: named queries under a prefix, each carrying a number,
applied once and recorded, described in the [Readme](https://github.com/botforge-pro/tory#readme).

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). A minor release here
can still ask something of you, up to and including reading your migrations against your live
schema. What a release asks is written in its own entry, so read the entry rather than judge by the
number. Entries start at 2.0.0, the first release of the current API.

## [2.4.0] - 2026-10-12

### Added

- Data migrations: named Go functions, run in order and recorded one by one, for the changes to
  data that one SQL statement cannot make. The
  [Readme](https://github.com/botforge-pro/tory#data-migrations) shows how to write and run them.

  `Load` and `ApplyPatches` are unchanged, and upgrading changes nothing until you call the new
  `DataMigrations.Apply`, which creates a `data_migrations` table on its first run. If your
  database already has a table of that name, look at it first. One whose primary key is a text
  `name`, beside an `applied_at` timestamp, is used as it stands, and every name in it counts as a
  migration already run. A table of any other shape makes `Apply` return an error, so rename it
  before the first call.

## [2.3.0] - 2026-09-10

### Changed

- One `-- name:` carries one statement, and a block holding a second is an error from `Load` naming
  the query and the count. 2.2.0 ended a query at the first `;` and dropped the rest of the block in
  silence, so a body written as three statements reached the server as one and nothing said so;
  2.1.0 and earlier ran all of them. Neither is right: PostgreSQL runs several statements in a body
  that binds nothing and refuses the same body the moment it takes an argument — measured, `cannot
  insert multiple commands into a prepared statement (SQLSTATE 42601)` — so a block that works today
  stops working when a `:name` is added to it.

  Running them in order instead was the other way out, and it is the one that cannot be relied on:
  the same body starts failing the day somebody adds an argument to it, and it fails against the
  server rather than at start-up. Refusing at `Load` puts the whole class in one place, before
  anything runs.

  Check before you upgrade rather than after: a `-- name:` block with a second `;` outside a `$$`
  body is one this release refuses. If you upgrade first, the program stops at start-up with

  ```
  query `add-provider-columns` in patches.sql holds 3 statements: give each one its own `-- name:`
  ```

  `Load` returns on the first one it meets rather than listing them, so a file with several offending
  blocks takes several rounds.

  What to do with a block depends on whether it is a patch, and on whether it has been applied:

  - **An ordinary query, or a patch no database has run yet.** Split it, giving each statement its
    own name, and call them in order.
  - **A patch already applied somewhere.** Delete the block, or reduce it to its first statement.
    Do not split it: its number is recorded, so new numbers would be handed to statements every
    existing database already ran, and those databases would skip them while a database patched
    later runs them.

    Deleting costs nothing, because a patch body only ever runs on a database that predates it. One
    that recorded the number will not run it again, and a database with no `db_version` rows is
    baselined to the latest version without a body running at all — a fresh install takes its whole
    shape from your declared schema and never reads the patch file for it.

    The recorded number stays spent whichever way you go, so number the next patch above the highest
    version any live database records, which is not always the highest number left in the file.

  Statements inside a `$$` block are untouched, as they were: their semicolons are not code, so a
  `do $$ … end $$;` body is one statement however much it holds. Wrapping several statements in one
  is a way to keep them under a single name where they belong together.

### Fixed

- A multi-statement body no longer reaches the server cut short with nothing said about it.

  What that leaves behind: a patch applied while 2.2.0 was in use is recorded as applied with only
  its first statement run, and `ApplyPatches` will not run it again. This release stops the next one
  from happening and repairs nothing already recorded, because what the missing statements were
  meant to do is not something the library can know.

  Whether it reached you turns on which version you built against when each patch first ran, not on
  the date: 2.1.0 and 2.2.0 were released the same day, and under 2.1.0 the whole body ran. The
  question to answer is "did any patch first run against 2.2.0", and your lockfile history answers
  it where the release dates cannot. Two cases are clear of it whatever the answer: a database whose
  patches all predate 2.2.0, and one whose multi-statement bodies were all `do $$ … end $$;`.

  Where a patch did run truncated, the schema tells you: read the statements after the first and ask
  the database whether each landed. Repair with a new patch at the end of the file, written to be
  right on a database that ran the whole body as well as one that ran part of it. Only databases
  that were already patched need it; a fresh one is baselined without running bodies and takes its
  shape from your declared schema.

- A statement left without its `;` before the next `-- name:` is now reported the way one at the end
  of a file already was. It used to go the way of everything else after the first `;`.

## [2.2.0] - 2026-09-08

> This release also truncated a `-- name:` block at its first statement without saying so, which the
> entry below did not notice and 2.3.0 describes. If you applied migrations while running it, read
> 2.3.0 before anything here.

### Changed

- `DbVersion` is now `DBVersion`, the spelling Go uses for initialisms. It is the type
  `ApplyPatches` returns, so code that names it changes by one letter; code that lets the type be
  inferred — `v, err := tory.ApplyPatches(ctx, t, opts)` — is unaffected.

### Fixed

- A query whose body never reaches a `;` is an error from `Load` now. It used to be dropped in
  silence and to surface much later, on the first call, as `query not found`.
- Quoted text is left to the database: `'strings'` with their doubled-quote escape, `"identifiers"`
  and `$$` blocks alike. `'note:hello'` no longer binds a variable called `hello`, `'a--b'` no longer
  loses its tail to comment stripping, `'a;b'` no longer ends the query, and an apostrophe in a
  comment no longer swallows everything up to the next quote. A `$$` block used to be spared only
  its semicolons, while `--` and `:name` inside it were still rewritten; now the whole block is left
  alone, so a `DO` body that carries a comment or a colon reaches the server as written.

  Worth a look after the upgrade: a query that leaned on the old behaviour now behaves differently,
  and one shape fails outright. A file whose last query ended at a `;` inside a literal used to load
  as a truncated query and now reaches the end without a terminator, so `Load` reports it and the
  program stops at startup instead of failing later against the server.

## [2.1.0] - 2026-09-08

### Added

- `t.Atomic(ctx, fn)` — a transaction as a method on the connection, next to the query methods that
  already live there.
- `tx.Atomic(ctx, inner)` nests a transaction inside a running one, which PostgreSQL runs as a
  savepoint: a batch the server rejects no longer costs the whole transaction, while a lost
  connection or a cancelled context still does. Both forms take
  the result type from the function they run — `Atomic[R]` with `R` inferred, which is why neither
  call spells a type argument, and a nested block that produces nothing returns `any` and `nil`.

  What a savepoint does and does not hold — a dead connection or a cancelled context is not one of
  the things it holds — and what nesting costs per block is written up in the
  [package documentation](https://pkg.go.dev/github.com/botforge-pro/tory/v2). Read it before
  building recovery on this.

  ```go
  report, err := t.Atomic(ctx, func(tx tory.Tx) (*Report, error) {
      _, err := tx.Atomic(ctx, func(tx tory.Tx) (any, error) {
          return nil, tx.Exec(ctx, "import-batch", tory.Args{"rows": batch.Rows})
      })
      ...
  })
  ```

### Changed

- The repository moved, so the import path moved with it. Every file that imports the package
  changes by that one line; calls, types and signatures stay as they are:

  ```go
  import "github.com/teamin-pro/tory/v2"   // was
  import "github.com/botforge-pro/tory/v2" // now
  ```

  Rewrite the imports, then run `go mod tidy`: it picks up the new path from them and drops the old
  requirement. `go get` on its own does not touch import lines.

  As a module, the new path begins at 2.1.0: the `go.mod` inside every earlier tag still declares
  the old one, so `@v2.0.0` under the new path fails with a module path mismatch. Builds pinned to
  the old path keep working — GitHub redirects it after the move, and the proxy already holds its
  tags — but 2.1.0 and everything after it is published under the new path only, so switching the
  import is the way to get them. As a repository, the move carried the tags along, which is why the
  links at the bottom of this file point at the new home for 2.0.0 as well.

  The module suffix stays `/v2`: the major version of the module did not change, only the host it
  is fetched from.

### Deprecated

- `tory.Atomic(ctx, t, fn)` in favour of `t.Atomic(ctx, fn)`. The old form keeps working through
  every 2.x release and goes away in 3.0.0.

## [2.0.0] - 2026-09-07

Every query call in your code changes. Go 1.27 or newer is required: the API is built on methods
with type parameters, which the language gained in that release.

Coming from 0.7.x, the import path gains the major-version suffix as well:

```go
import "github.com/teamin-pro/tory"    // was, up to 0.7.4
import "github.com/teamin-pro/tory/v2" // 2.0.0; see 2.1.0 above for where it lives now
```

### Changed

- Queries became methods and take a `context.Context` first. The connection and the transaction
  carry the same set, except `Query`, which belongs to the transaction alone — as it did before,
  though it changed shape with the rest: `tx.Query(name, args, scan)` is now
  `tx.Query[T](ctx, name, args, scan)`.

  ```go
  users, err := tory.Select[User](t, "list-users", nil) // was
  users, err := t.Select[User](ctx, "list-users", nil)  // now
  ```

- A transaction is no longer bound to one row type. `Tx[T]` became a plain `Tx`, and the type
  parameter moved to the methods that return rows — `Select`, `Get`, `Scalar` and `Query` — so one
  transaction reads as many different types as it needs. `Exec`, `ExecReturning` and `QueryRow` are
  methods on it too, they just have no type parameter to move.

  ```go
  tory.Atomic(t, func(tx tory.Tx[User]) (*User, error) {      // was
      return tx.Get("get-user-by-id", tory.Args{"id": 42})
  })
  tory.Atomic(ctx, t, func(tx tory.Tx) (*User, error) {       // now
      return tx.Get[User](ctx, "get-user-by-id", tory.Args{"id": 42})
  })
  ```

- `ApplyPatches(ctx, t, opts)` and `Atomic(ctx, t, fn)` take a context too, and `Atomic[R, T]`
  became `Atomic[R]`: the row type left with `Tx[T]`, the result type stayed.

### Removed

- The package-level query functions `Select`, `Get`, `Scalar`, `QueryRow`, `Exec` and
  `ExecReturning`. Each is now a method of the same name on the connection and on the transaction,
  so the replacement is mechanical.

[2.4.0]: https://github.com/botforge-pro/tory/compare/v2.3.0...v2.4.0
[2.3.0]: https://github.com/botforge-pro/tory/compare/v2.2.0...v2.3.0
[2.2.0]: https://github.com/botforge-pro/tory/compare/v2.1.0...v2.2.0
[2.1.0]: https://github.com/botforge-pro/tory/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/botforge-pro/tory/releases/tag/v2.0.0
