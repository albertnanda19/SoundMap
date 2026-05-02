package migrations

import (
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// RunMigrations runs all pending database migrations
func RunMigrations(databaseURL, migrationsPath string, logger *slog.Logger) error {
	m, err := migrate.New(
		"file://"+migrationsPath,
		databaseURL,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		if err == migrate.ErrNoChange {
			logger.Info("no new migrations to apply")
		} else {
			return fmt.Errorf("failed to run migrations: %w", err)
		}
	}

	version, dirty, err := m.Version()
	if err != nil {
		if err == migrate.ErrNilVersion {
			logger.Info("no migrations applied yet")
		} else {
			logger.Warn("could not get migration version", slog.String("error", err.Error()))
		}
	} else {
		logger.Info("migrations completed",
			slog.Uint64("version", uint64(version)),
			slog.Bool("dirty", dirty))
	}

	return nil
}
