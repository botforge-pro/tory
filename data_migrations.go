package tory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DataMigrationFunc does one data migration and says in a line what it did.
// It runs outside any transaction of the runner's, so a long one can commit in
// batches of its own.
type DataMigrationFunc func(ctx context.Context) (string, error)

type AppliedDataMigration struct {
	Name      string    `db:"name"`
	AppliedAt time.Time `db:"applied_at"`
}

type DataMigrationsOptions struct {
	OnSkip   func(name string)
	OnStart  func(name string)
	OnFinish func(name, report string)
}

// DataMigrations is a list of named data migrations, run in the order they
// were added. Build it with NewDataMigrations, fill it with Add and run it
// with Apply.
type DataMigrations struct {
	opts  DataMigrationsOptions
	steps []dataMigration
}

const closeTimeout = 5 * time.Second

type dataMigration struct {
	name string
	run  DataMigrationFunc
}

func NewDataMigrations(opts DataMigrationsOptions) *DataMigrations {
	return &DataMigrations{opts: opts}
}

// Add appends a migration to the list. A blank or repeated name is reported
// by Apply, before any migration runs.
func (m *DataMigrations) Add(name string, run DataMigrationFunc) {
	m.steps = append(m.steps, dataMigration{name: name, run: run})
}

// Apply runs, in order, every migration not yet recorded in the
// data_migrations table, and records each one as it finishes. The first that
// fails stops the run: it stays unrecorded, the ones after it do not start,
// and its error is returned. Apply returns every migration recorded by the
// time it stopped.
//
// A run holds a database lock from start to end, so a second process calling
// Apply at the same time waits for the first and then skips what it recorded.
// The lock lives on a connection of its own, opened beside the pool with the
// pool's settings and closed when the run ends, so it takes no connection the
// migrations need, however small the pool.
func (m *DataMigrations) Apply(ctx context.Context, t Tory) (applied []AppliedDataMigration, err error) {
	if err := m.check(); err != nil {
		return nil, err
	}
	db := New(t.pool)
	if err := db.Load(sqlFiles); err != nil {
		return nil, err
	}

	conn, err := pgx.ConnectConfig(ctx, t.pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, fmt.Errorf("data migrations: open a connection for the lock: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		if closeErr := conn.Close(ctx); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("data migrations: close the lock's connection: %w", closeErr))
		}
	}()
	if _, err := execReturning(ctx, db, conn, "tory.lock-data-migrations", nil); err != nil {
		return nil, err
	}

	return m.applyLocked(ctx, db, conn)
}

func (m *DataMigrations) applyLocked(ctx context.Context, db Tory, conn runner) ([]AppliedDataMigration, error) {
	if _, err := execReturning(ctx, db, conn, "tory.create-table-data-migrations", nil); err != nil {
		return nil, err
	}

	applied, err := selectRows[AppliedDataMigration](ctx, db, conn, "tory.list-data-migrations", nil)
	if err != nil {
		return nil, err
	}
	done := make(map[string]bool, len(applied))
	for _, a := range applied {
		done[a.Name] = true
	}

	for _, step := range m.steps {
		if done[step.name] {
			if m.opts.OnSkip != nil {
				m.opts.OnSkip(step.name)
			}
			continue
		}

		if m.opts.OnStart != nil {
			m.opts.OnStart(step.name)
		}
		report, err := step.run(ctx)
		if err != nil {
			return applied, fmt.Errorf("data migration `%s`: %w", step.name, err)
		}

		recorded, err := getRow[AppliedDataMigration](ctx, db, conn, "tory.record-data-migration", Args{"name": step.name})
		if err != nil {
			return applied, fmt.Errorf("record data migration `%s`: %w", step.name, err)
		}
		applied = append(applied, *recorded)

		if m.opts.OnFinish != nil {
			m.opts.OnFinish(step.name, report)
		}
	}

	return applied, nil
}

func (m *DataMigrations) check() error {
	seen := make(map[string]bool, len(m.steps))
	for _, step := range m.steps {
		if step.name == "" {
			return fmt.Errorf("data migration with a blank name")
		}
		if step.run == nil {
			return fmt.Errorf("data migration `%s` has nothing to run", step.name)
		}
		if seen[step.name] {
			return fmt.Errorf("duplicate data migration: `%s`", step.name)
		}
		seen[step.name] = true
	}
	return nil
}
