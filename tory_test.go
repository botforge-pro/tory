package tory

import (
	"context"
	"embed"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed test.sql
var testFiles embed.FS

func TestParse(t *testing.T) {
	db := New(newPool(t))

	require.NoError(t, db.Load(testFiles))
	assert.Len(t, db.AllQueries(), 12)

	t.Run("remove comments", func(t *testing.T) {
		q, err := db.Query("test-comments")
		require.NoError(t, err)
		assert.Equal(t, "SELECT 1 + 2", q.Body())
	})

	t.Run("remove comments after semicolon", func(t *testing.T) {
		q, err := db.Query("test-comments-after-semicolon")
		require.NoError(t, err)
		assert.Equal(t, "SELECT 1 + 2", q.Body())
	})

	t.Run("arguments", func(t *testing.T) {
		q, err := db.Query("test-arguments")
		require.NoError(t, err)
		assert.Equal(t, "SELECT name FROM users WHERE id = $1 AND name ILIKE $2", q.Body())
		assert.Equal(t, []any{42, "Alice"}, q.Args(Args{"id": 42, "q": "Alice"}))
	})

	t.Run("name", func(t *testing.T) {
		_, err := db.Query("test.name-with-dots")
		require.NoError(t, err)
	})

	t.Run("exec", func(t *testing.T) {
		var res int
		err := db.QueryRow(t.Context(), "test-sum", Args{"x": 1, "y": 2}, &res)
		require.NoError(t, err)
		assert.Equal(t, 3, res)
	})

	t.Run("generic method", func(t *testing.T) {
		res, err := db.Scalar[int](t.Context(), "test-sum", Args{"x": 2, "y": 3})
		require.NoError(t, err)
		assert.Equal(t, 5, res)
	})

	t.Run("generic transaction method", func(t *testing.T) {
		res, err := Atomic(t.Context(), db, func(tx Tx) (int, error) {
			return tx.Scalar[int](t.Context(), "test-sum", Args{"x": 3, "y": 4})
		})
		require.NoError(t, err)
		assert.Equal(t, 7, res)
	})

	t.Run("transaction as a method", func(t *testing.T) {
		res, err := db.Atomic(t.Context(), func(tx Tx) (int, error) {
			return tx.Scalar[int](t.Context(), "test-sum", Args{"x": 4, "y": 5})
		})
		require.NoError(t, err)
		assert.Equal(t, 9, res)
	})

	t.Run("failed nested transaction keeps the outer one", func(t *testing.T) {
		count, err := db.Atomic(t.Context(), func(tx Tx) (int, error) {
			if err := tx.Exec(t.Context(), "test-create-temp-table", nil); err != nil {
				return 0, err
			}
			if err := tx.Exec(t.Context(), "test-insert", Args{"n": 1}); err != nil {
				return 0, err
			}

			_, nestedErr := tx.Atomic(t.Context(), func(nested Tx) (int, error) {
				if err := nested.Exec(t.Context(), "test-insert", Args{"n": 2}); err != nil {
					return 0, err
				}

				inserted, err := nested.Scalar[int](t.Context(), "test-count", nil)
				if err != nil {
					return 0, err
				}
				require.Equal(t, 2, inserted)

				return 0, errors.New("roll back the savepoint")
			})
			require.Error(t, nestedErr)

			return tx.Scalar[int](t.Context(), "test-count", nil)
		})
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("successful nested transaction is released", func(t *testing.T) {
		count, err := db.Atomic(t.Context(), func(tx Tx) (int, error) {
			if err := tx.Exec(t.Context(), "test-create-temp-table", nil); err != nil {
				return 0, err
			}

			if _, err := tx.Atomic(t.Context(), func(nested Tx) (any, error) {
				return nil, nested.Exec(t.Context(), "test-insert", Args{"n": 1})
			}); err != nil {
				return 0, err
			}

			return tx.Scalar[int](t.Context(), "test-count", nil)
		})
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("dollar-quoted block keeps inner semicolons", func(t *testing.T) {
		q, err := db.Query("test-do-block")
		require.NoError(t, err)
		assert.Equal(t,
			"do $$ begin create type test_dollar_color as enum ('red', 'green'); "+
				"exception when duplicate_object then null; end $$",
			q.Body())
	})

	t.Run("dollar-quoted block with multiple statements", func(t *testing.T) {
		q, err := db.Query("test-do-block-multiple-statements")
		require.NoError(t, err)
		assert.Equal(t,
			"do $$ begin perform 1; perform 2; end $$",
			q.Body())
	})
}

func BenchmarkExec(b *testing.B) {
	db := New(newPool(b))
	require.NoError(b, db.Load(testFiles))

	var res int

	// warmup
	err := db.QueryRow(context.Background(), "test-sum", Args{"x": 1, "y": 2}, &res)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		err := db.QueryRow(context.Background(), "test-sum", Args{"x": 1, "y": 2}, &res)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func newPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://tory:tory@localhost:5432")
	require.NoError(t, err)
	return pool
}
