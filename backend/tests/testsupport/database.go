// Package testsupport contains test-only database infrastructure. It is not
// imported by application packages and never reads the development DATABASE_URL.
package testsupport

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func ValidateDSN(dsn string) error {
	target, err := url.Parse(dsn)
	if err != nil || (target.Scheme != "postgres" && target.Scheme != "postgresql") {
		return fmt.Errorf("test database must use a PostgreSQL URL")
	}
	name := strings.TrimPrefix(target.Path, "/")
	if !strings.HasSuffix(name, "_test") && !strings.HasSuffix(name, "_tests") && !strings.HasPrefix(name, "test_") {
		return fmt.Errorf("refusing integration database without a test-specific name")
	}
	if target.Query().Get("dbname") != "" || target.Query().Get("database") != "" {
		return fmt.Errorf("test database name overrides are not allowed")
	}
	return nil
}
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("OPS_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("REQUIRE_INTEGRATION_TESTS") == "1" {
			t.Fatal("OPS_TEST_DATABASE_URL is required for this run")
		}
		t.Skip("integration test: run scripts/check.sh with its dedicated test database")
	}
	if err := ValidateDSN(dsn); err != nil {
		t.Fatal(err)
	}
	return dsn
}
func Migrations() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "../../sql/migrations")
}
func Migrate(ctx context.Context, database *sql.DB) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, database, Migrations())
}

// IsolatedDatabase gives durable worker tests their own schema and uses Goose's
// migration runner, rather than a hand-written approximation of the DDL.
func IsolatedDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := DSN(t)
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "stage9_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	var database *sql.DB
	t.Cleanup(func() {
		if database != nil {
			database.Close()
		}
		_, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	target, _ := url.Parse(dsn)
	query := target.Query()
	query.Set("search_path", schema)
	target.RawQuery = query.Encode()
	database, err = sql.Open("pgx", target.String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	return database
}
