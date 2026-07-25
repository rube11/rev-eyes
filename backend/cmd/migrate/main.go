package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/database"
)

var migrationNamePattern = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.sql$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: go run ./cmd/migrate migrations/NNNN_name.sql")
	}
	migrationPath := strings.TrimSpace(os.Args[1])
	migrationName := filepath.Base(migrationPath)
	if !migrationNamePattern.MatchString(migrationName) {
		return errors.New("migration filename must use NNNN_lowercase_name.sql")
	}
	migrationSQL, err := os.ReadFile(migrationPath)
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	if strings.TrimSpace(string(migrationSQL)) == "" {
		return errors.New("migration is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(
		ctx,
		`select pg_advisory_xact_lock(hashtext('rev-eyes-schema-migrations'))`,
	); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	if _, err := tx.Exec(
		ctx,
		`create table if not exists public.schema_migrations (
		     version text primary key,
		     applied_at timestamptz not null default now()
		 )`,
	); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	var applied bool
	if err := tx.QueryRow(
		ctx,
		`select exists (
		     select 1
		     from public.schema_migrations
		     where version = $1
		 )`,
		migrationName,
	).Scan(&applied); err != nil {
		return fmt.Errorf("check migration ledger: %w", err)
	}
	if applied {
		fmt.Printf("%s already applied\n", migrationName)
		return tx.Rollback(ctx)
	}

	if _, err := tx.Exec(ctx, string(migrationSQL)); err != nil {
		return fmt.Errorf("apply %s: %w", migrationName, err)
	}
	if _, err := tx.Exec(
		ctx,
		`insert into public.schema_migrations (version) values ($1)`,
		migrationName,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	fmt.Printf("%s applied\n", migrationName)
	return nil
}
