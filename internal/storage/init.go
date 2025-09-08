// internal/storage/init_storage.go
package storage

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5"
	_ "github.com/lib/pq"
)

const migrationPath = "migrations"

func runMigrations(db *sql.DB) error {
	const op = "storage.migrations"

	goose.SetDialect("postgres")

	err := goose.Up(db, migrationPath)
	if err != nil {
		if err == goose.ErrNoNextVersion {
			log.Println("No migrations to apply.")
			return nil
		}
		return fmt.Errorf("%s: %v", op, err)
	}
	log.Println("Database migrations applied successfully.")
	return nil
}
