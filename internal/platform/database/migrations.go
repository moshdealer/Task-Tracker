package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"task-tracker/internal/platform/config"
)

// Migrate применяет все ожидающие миграции из migrationFiles через отдельное подключение
func Migrate(ctx context.Context, cfg config.Database, migrationFiles fs.FS) error {
	hasMigrations, err := containsMigrations(migrationFiles)
	if err != nil {
		return fmt.Errorf("database: inspect migrations: %w", err)
	}
	if !hasMigrations {
		return nil
	}

	sourceDriver, err := iofs.New(migrationFiles, ".")
	if err != nil {
		return fmt.Errorf("database: initialize migration source: %w", err)
	}

	migrationDB, err := open(cfg.URL, true)
	if err != nil {
		_ = sourceDriver.Close()
		return err
	}
	defer migrationDB.Close()

	connection, err := migrationDB.Conn(ctx)
	if err != nil {
		_ = sourceDriver.Close()
		return fmt.Errorf("database: acquire migration connection: %w", err)
	}

	databaseDriver, err := migratemysql.WithConnection(ctx, connection, &migratemysql.Config{})
	if err != nil {
		_ = connection.Close()
		_ = sourceDriver.Close()
		return fmt.Errorf("database: initialize MySQL migration driver: %w", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", sourceDriver, "mysql", databaseDriver)
	if err != nil {
		_ = databaseDriver.Close()
		_ = sourceDriver.Close()
		return fmt.Errorf("database: initialize migrator: %w", err)
	}

	migrateErr := migrator.Up()
	sourceCloseErr, databaseCloseErr := migrator.Close()

	if migrateErr != nil && !errors.Is(migrateErr, migrate.ErrNoChange) {
		return fmt.Errorf("database: apply migrations: %w", migrateErr)
	}
	if sourceCloseErr != nil {
		return fmt.Errorf("database: close migration source: %w", sourceCloseErr)
	}
	if databaseCloseErr != nil {
		return fmt.Errorf("database: close migration connection: %w", databaseCloseErr)
	}

	return nil
}

func containsMigrations(migrationFiles fs.FS) (bool, error) {
	entries, err := fs.ReadDir(migrationFiles, ".")
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, err := source.DefaultParse(entry.Name()); err == nil {
			return true, nil
		}
	}

	return false, nil
}
