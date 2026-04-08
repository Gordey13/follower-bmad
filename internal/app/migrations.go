package app

import (
	migrate "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// migrationBootstrapLinker pins migration dependencies required by scripts/bootstrap tooling.
func migrationBootstrapLinker() error {
	return migrate.ErrNoChange
}
