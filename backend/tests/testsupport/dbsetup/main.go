package main

import (
	"context"
	"database/sql"
	"github.com/kryptonn36/multiWorks/tests/testsupport"
	"log"
	"os"
	"time"
)

func main() {
	dsn := os.Getenv("OPS_TEST_DATABASE_URL")
	if err := testsupport.ValidateDSN(dsn); err != nil {
		log.Fatal(err)
	}
	database, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal("open test database failed")
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = testsupport.Migrate(ctx, database); err != nil {
		log.Fatal(err)
	}
}
