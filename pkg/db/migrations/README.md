# Database Migrations

Migrations are Go files, not `.sql` files: the SQL is compiled into the single binary, with no `embed.FS` and no migration library.

## How it works

- Each file defines one `Migration{Version, Description, SQL []string}`; `migrations.go` lists them in order in `schemas`.
- `db.New` calls `migrations.Run`, which creates `schema_migrations` if needed and applies every version not yet recorded there, each in its own transaction.
- Versions are tracked one by one, so a lower-numbered migration added later still runs.
- A database recording a version this build doesn't know is refused (`ErrNewerSchema`).
- An existing library is backed up (`.wandersort.db.pre-upgrade.zst`) before any pending migration runs.

## Adding a migration

Create `00N_<name>.go` with the next version and append it to `schemas`. Never reorder or edit an entry that has shipped.

A database that already recorded a version never sees a change to it, so `TestShippedMigrationsNeverChange` pins every migration a release has carried. When a release ships a new one, add its hash there (the failing test prints it).

Never drop a table that others reference: with foreign keys on, the drop deletes its rows first and cascades. Alter columns in place (`004_file_identity.go`).

`Unless` is for a schema that may already have what the migration makes; the version is then recorded without running it.

The schema is the `CREATE TABLE` statements in `001_scanner.go`, `002_file_metadata.go` and `003_vfs.go`, altered by the versions after them.
