package database

import (
	"context"
	"database/sql"
	"fmt"

	mysqldriver "github.com/go-sql-driver/mysql"
	"task-tracker/internal/platform/config"
)

// Open создаёт пул подключений и проверяет доступность MySQL
func Open(ctx context.Context, cfg config.Database) (*sql.DB, error) {
	db, err := open(cfg.URL, false)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database: ping MySQL: %w", err)
	}

	return db, nil
}

func open(dsn string, multiStatements bool) (*sql.DB, error) {
	driverConfig, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("database: parse MySQL DSN: %w", err)
	}
	driverConfig.MultiStatements = multiStatements

	connector, err := mysqldriver.NewConnector(driverConfig)
	if err != nil {
		return nil, fmt.Errorf("database: create MySQL connector: %w", err)
	}

	return sql.OpenDB(connector), nil
}
