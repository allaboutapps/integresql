// Package integresql_test provides benchmarks to test integresql performance.
// Before running any of the tests, make sure that integresql is running.
package integresql_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/tests/testclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func BenchmarkGetDatabaseFromNewTemplate(b *testing.B) {
	ctx := context.Background()
	client, err := testclient.DefaultClientFromEnv()
	require.NoError(b, err)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			newTemplateHash := uuid.NewString()

			err := client.SetupTemplateWithDBClient(ctx, newTemplateHash, func(db *sql.DB) error {
				_, err := db.ExecContext(ctx, `CREATE TABLE users (
			id int NOT NULL,
			username varchar(255) NOT NULL,
			created_at timestamptz NOT NULL,
			CONSTRAINT users_pkey PRIMARY KEY (id));`)
				require.NoError(b, err)
				res, err := db.ExecContext(ctx, `
			INSERT INTO users (id, username, created_at)
			VALUES
				(1, 'user1', $1),
				(2, 'user2', $1);
			`, time.Now())
				require.NoError(b, err)
				inserted, err := res.RowsAffected()
				require.NoError(b, err)
				require.Equal(b, int64(2), inserted)
				return nil
			})
			require.NoError(b, err)

			dbConfig, err := client.GetTestDatabase(ctx, newTemplateHash)
			require.NoError(b, err)
			db, err := sql.Open("postgres", dbConfig.Config.ConnectionString())
			require.NoError(b, err)
			defer db.Close()

			require.NoError(b, db.PingContext(ctx))
			row := db.QueryRowContext(ctx, "SELECT COUNT(id) FROM users;")
			require.NoError(b, row.Err())
			var userCnt int
			require.NoError(b, row.Scan(&userCnt))
			assert.Equal(b, 2, userCnt)
			db.Close()

			require.NoError(b, client.ReturnTestDatabase(ctx, newTemplateHash, dbConfig.ID))
			require.NoError(b, client.DiscardTemplate(ctx, newTemplateHash))
		}
	})

}

func BenchmarkGetDatabaseFromExistingTemplate(b *testing.B) {
	ctx := context.Background()
	client, err := testclient.DefaultClientFromEnv()
	require.NoError(b, err)

	newTemplateHash := uuid.NewString()
	err = client.SetupTemplateWithDBClient(ctx, newTemplateHash, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE users (
			id int NOT NULL,
			username varchar(255) NOT NULL,
			created_at timestamptz NOT NULL,
			CONSTRAINT users_pkey PRIMARY KEY (id));`)
		require.NoError(b, err)
		res, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, created_at)
		VALUES
			(1, 'user1', $1);
	`, time.Now())
		require.NoError(b, err)
		inserted, err := res.RowsAffected()
		require.NoError(b, err)
		require.Equal(b, int64(1), inserted)
		return nil
	})
	require.NoError(b, err)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {

			dbConfig, err := client.GetTestDatabase(ctx, newTemplateHash)
			require.NoError(b, err)
			db, err := sql.Open("postgres", dbConfig.Config.ConnectionString())
			require.NoError(b, err)
			defer db.Close()

			require.NoError(b, db.PingContext(ctx))
			row := db.QueryRowContext(ctx, "SELECT COUNT(id) FROM users;")
			require.NoError(b, row.Err())
			var userCnt int
			require.NoError(b, row.Scan(&userCnt))
			assert.Equal(b, 1, userCnt)
			db.Close()

			require.NoError(b, client.ReturnTestDatabase(ctx, newTemplateHash, dbConfig.ID))
		}
	})

	b.Cleanup(func() { require.NoError(b, client.DiscardTemplate(ctx, newTemplateHash)) })
}

// nolint: deadcode
func ignoreError(toIgnore error, err error) error {
	if errors.Is(err, toIgnore) {
		return nil
	}

	return err
}
