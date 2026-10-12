package tory

import (
	"context"
	"errors"
	"fmt"
	syncatomic "sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataMigrations_RunOnceAndAreRecorded(t *testing.T) {
	db := dbWithoutDataMigrations(t)
	runs := 0
	m := NewDataMigrations(DataMigrationsOptions{})
	m.Add("counted", func(context.Context) (string, error) {
		runs++
		return "ran", nil
	})

	_, err := m.Apply(t.Context(), db)
	require.NoError(t, err)
	applied, err := m.Apply(t.Context(), db)
	require.NoError(t, err)

	assert.Equal(t, 1, runs)
	require.Len(t, applied, 1)
	assert.Equal(t, "counted", applied[0].Name)
}

func TestDataMigrations_TheFirstFailureStopsTheRun(t *testing.T) {
	db := dbWithoutDataMigrations(t)
	var ran []string
	refuse := true
	m := NewDataMigrations(DataMigrationsOptions{})
	m.Add("first", func(context.Context) (string, error) {
		ran = append(ran, "first")
		return "", nil
	})
	m.Add("second", func(context.Context) (string, error) {
		ran = append(ran, "second")
		if refuse {
			return "", errors.New("the bucket said no")
		}
		return "", nil
	})
	m.Add("third", func(context.Context) (string, error) {
		ran = append(ran, "third")
		return "", nil
	})

	applied, err := m.Apply(t.Context(), db)
	require.ErrorContains(t, err, "second")
	require.ErrorContains(t, err, "the bucket said no")
	assert.Equal(t, []string{"first", "second"}, ran,
		"a migration after a failed one may rely on what the failed one was to do")
	require.Len(t, applied, 1)
	assert.Equal(t, "first", applied[0].Name)

	refuse = false
	ran = nil
	applied, err = m.Apply(t.Context(), db)
	require.NoError(t, err)
	assert.Equal(t, []string{"second", "third"}, ran,
		"the failed one was not recorded, so the next run starts from it")
	assert.Len(t, applied, 3)
}

func TestDataMigrations_AMigrationThatCannotBeRecordedStopsTheRun(t *testing.T) {
	db := dbWithoutDataMigrations(t)
	require.NoError(t, db.Exec(t.Context(), "test-create-data-migrations-refusing-a-name", nil))
	var ran []string
	m := NewDataMigrations(DataMigrationsOptions{})
	m.Add("refused", func(context.Context) (string, error) {
		ran = append(ran, "refused")
		return "", nil
	})
	m.Add("after", func(context.Context) (string, error) {
		ran = append(ran, "after")
		return "", nil
	})

	applied, err := m.Apply(t.Context(), db)

	require.ErrorContains(t, err, "record data migration `refused`")
	assert.Equal(t, []string{"refused"}, ran, "a migration after one that went unrecorded ran")
	assert.Empty(t, applied)
}

func TestDataMigrations_TwoRunsAtOnceDoAMigrationOnce(t *testing.T) {
	db := dbWithoutDataMigrations(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var runs syncatomic.Int32
	m := NewDataMigrations(DataMigrationsOptions{})
	m.Add("slow", func(context.Context) (string, error) {
		runs.Add(1)
		started <- struct{}{}
		<-release
		return "", nil
	})

	first := make(chan error, 1)
	go func() {
		_, err := m.Apply(t.Context(), db)
		first <- err
	}()
	<-started
	second := make(chan error, 1)
	go func() {
		_, err := m.Apply(t.Context(), db)
		second <- err
	}()
	require.Eventually(t, func() bool {
		waiting, err := db.Scalar[int](t.Context(), "test-count-advisory-lock-waiters", nil)
		return err == nil && waiting == 1
	}, 5*time.Second, 10*time.Millisecond, "the second run never queued behind the first")
	close(release)

	require.NoError(t, <-first)
	require.NoError(t, <-second)
	assert.Equal(t, int32(1), runs.Load(),
		"two instances starting during one deploy both ran the migration")
}

func TestDataMigrations_HooksHearEveryMigration(t *testing.T) {
	db := dbWithoutDataMigrations(t)
	var heard []string
	opts := DataMigrationsOptions{
		OnSkip:   func(name string) { heard = append(heard, "skip "+name) },
		OnStart:  func(name string) { heard = append(heard, "start "+name) },
		OnFinish: func(name, report string) { heard = append(heard, "finish "+name+": "+report) },
	}
	first := NewDataMigrations(opts)
	first.Add("old", func(context.Context) (string, error) { return "moved 2", nil })
	_, err := first.Apply(t.Context(), db)
	require.NoError(t, err)

	heard = nil
	second := NewDataMigrations(opts)
	second.Add("old", func(context.Context) (string, error) { return "moved 2", nil })
	second.Add("new", func(context.Context) (string, error) { return "moved 3", nil })
	_, err = second.Apply(t.Context(), db)
	require.NoError(t, err)

	assert.Equal(t, []string{"skip old", "start new", "finish new: moved 3"}, heard)
}

func TestDataMigrations_AFaultyListRunsNothing(t *testing.T) {
	for name, add := range map[string]func(*DataMigrations, DataMigrationFunc){
		"blank name": func(m *DataMigrations, run DataMigrationFunc) {
			m.Add("", run)
		},
		"repeated name": func(m *DataMigrations, run DataMigrationFunc) {
			m.Add("twice", run)
			m.Add("twice", run)
		},
		"nothing to run": func(m *DataMigrations, _ DataMigrationFunc) {
			m.Add("empty", nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := dbWithoutDataMigrations(t)
			runs := 0
			m := NewDataMigrations(DataMigrationsOptions{})
			m.Add("before", func(context.Context) (string, error) {
				runs++
				return "", nil
			})
			add(m, func(context.Context) (string, error) { return "", nil })

			_, err := m.Apply(t.Context(), db)
			require.Error(t, err)
			assert.Zero(t, runs)
		})
	}
}

func TestDataMigrations_RunOnAPoolOfOneConnection(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://tory:tory@localhost:5432?pool_max_conns=1")
	require.NoError(t, err)
	var connected syncatomic.Int32
	cfg.AfterConnect = func(context.Context, *pgx.Conn) error {
		connected.Add(1)
		return nil
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	db := New(pool)
	require.NoError(t, db.Load(testFiles))
	require.NoError(t, db.Exec(t.Context(), "test-drop-data-migrations", nil))
	m := NewDataMigrations(DataMigrationsOptions{})
	m.Add("queries the pool", func(ctx context.Context) (string, error) {
		sum, err := db.Scalar[int](ctx, "test-sum", Args{"x": 1, "y": 2})
		return fmt.Sprintf("summed %d", sum), err
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	applied, err := m.Apply(ctx, db)

	require.NoError(t, err,
		"the lock took the pool's only connection, and the migration waited for it until the deadline")
	assert.Len(t, applied, 1)
	assert.Equal(t, int32(2), connected.Load(),
		"the lock's connection skipped the pool's AfterConnect, which may set the search_path or "+
			"role the data_migrations table is to be found under")
}

func TestDataMigrations_ARunCancelledWhileWaitingLeavesNoLockBehind(t *testing.T) {
	db := dbWithoutDataMigrations(t)
	release := make(chan struct{})
	started := make(chan struct{})
	holder := NewDataMigrations(DataMigrationsOptions{})
	holder.Add("holds the lock", func(context.Context) (string, error) {
		close(started)
		<-release
		return "", nil
	})
	held := make(chan error, 1)
	go func() {
		_, err := holder.Apply(t.Context(), db)
		held <- err
	}()
	<-started

	runs := 0
	waiter := NewDataMigrations(DataMigrationsOptions{})
	waiter.Add("waits", func(context.Context) (string, error) {
		runs++
		return "", nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	waited := make(chan error, 1)
	go func() {
		_, err := waiter.Apply(ctx, db)
		waited <- err
	}()
	require.Eventually(t, func() bool {
		waiting, err := db.Scalar[int](t.Context(), "test-count-advisory-lock-waiters", nil)
		return err == nil && waiting == 1
	}, 5*time.Second, 10*time.Millisecond, "the second run never queued behind the first")
	cancel()
	require.ErrorIs(t, <-waited, context.Canceled)
	close(release)
	require.NoError(t, <-held)

	after, err := waiter.Apply(t.Context(), db)

	require.NoError(t, err, "the cancelled wait left the lock taken")
	assert.Equal(t, 1, runs)
	assert.Len(t, after, 2)
}

func dbWithoutDataMigrations(t *testing.T) Tory {
	t.Helper()
	db := New(newPool(t))
	require.NoError(t, db.Load(testFiles))
	require.NoError(t, db.Exec(t.Context(), "test-drop-data-migrations", nil))
	return db
}
