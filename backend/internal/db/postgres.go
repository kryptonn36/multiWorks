package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func Open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	database.SetMaxOpenConns(20)
	database.SetMaxIdleConns(5)
	database.SetConnMaxLifetime(30 * time.Minute)

	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}

	log.Println("Connected to PostgreSQL")

	if err := goose.SetDialect("postgres"); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("configure migrations: %w", err)
	}

	if err := goose.UpContext(ctx, database, "migrations"); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("run database migrations: %w", err)
	}

	log.Println("Database migrations are up to date")

	return database, nil
}
