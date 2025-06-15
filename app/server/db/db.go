package db

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

var Conn *sqlx.DB

const LockTimeout = 4000
const IdleInTransactionSessionTimeout = 90000
const StatementTimeout = 30000

func Connect() error {
	var err error

	dbUrl := os.Getenv("DATABASE_URL")
	if dbUrl == "" {
		if os.Getenv("DB_HOST") != "" &&
			os.Getenv("DB_PORT") != "" &&
			os.Getenv("DB_USER") != "" &&
			os.Getenv("DB_PASSWORD") != "" &&
			os.Getenv("DB_NAME") != "" {
			encodedPassword := url.QueryEscape(os.Getenv("DB_PASSWORD"))

			dbUrl = "postgres://" + os.Getenv("DB_USER") + ":" + encodedPassword + "@" + os.Getenv("DB_HOST") + ":" + os.Getenv("DB_PORT") + "/" + os.Getenv("DB_NAME")
		}

		if dbUrl == "" {
			return errors.New("DATABASE_URL or DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, and DB_NAME environment variables must be set")
		}
	}

	if strings.Contains(dbUrl, "?") {
		dbUrl += fmt.Sprintf("&statement_timeout=%d&lock_timeout=%d&timezone=UTC&idle_in_transaction_session_timeout=%d", StatementTimeout, LockTimeout, IdleInTransactionSessionTimeout)
	} else {
		dbUrl += fmt.Sprintf("?statement_timeout=%d&lock_timeout=%d&timezone=UTC&idle_in_transaction_session_timeout=%d", StatementTimeout, LockTimeout, IdleInTransactionSessionTimeout)
	}

	Conn, err = sqlx.Connect("postgres", dbUrl)
	if err != nil {
		return err
	}

	log.Println("connected to database")

	if os.Getenv("GOENV") == "production" {
		Conn.SetMaxOpenConns(50)
		Conn.SetMaxIdleConns(20)
	} else {
		Conn.SetMaxOpenConns(10)
		Conn.SetMaxIdleConns(5)
	}

	// Verify settings
	type setting struct {
		Name    string  `db:"name"`
		Setting string  `db:"setting"`
		Unit    *string `db:"unit"`
		Context string  `db:"context"`
	}

	var settings []setting
	err = Conn.Select(&settings, `
		SELECT name, setting, unit, context 
		FROM pg_settings 
		WHERE name IN ('statement_timeout', 'lock_timeout', 'TimeZone', 'idle_in_transaction_session_timeout')
`)
	if err != nil {
		return fmt.Errorf("error checking settings: %v", err)
	}

	s := ""
	for _, setting := range settings {
		unitStr := ""
		if setting.Unit != nil {
			unitStr = " " + *setting.Unit // Add a leading space only if there's a unit
		}
		s += fmt.Sprintf("- %s = %s%s (context: %s)\n", setting.Name, setting.Setting, unitStr, setting.Context)
	}
	log.Printf("\n\nDatabase settings:\n%s\n", s)

	return nil
}

func MigrationsUp() error {
	migrationsDir := "migrations"
	if os.Getenv("MIGRATIONS_DIR") != "" {
		migrationsDir = os.Getenv("MIGRATIONS_DIR")
	}

	return migrationsUp(migrationsDir)
}

func MigrationsUpWithDir(dir string) error {
	return migrationsUp(dir)
}

func migrationsUp(dir string) error {
	// Create migrations table if it doesn't exist
	_, err := Conn.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("error creating migrations table: %v", err)
	}

	// Get list of applied migrations
	appliedMigrations := make(map[string]bool)
	rows, err := Conn.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("error querying applied migrations: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("error scanning migration version: %v", err)
		}
		appliedMigrations[version] = true
	}

	// Read migration files from directory
	files, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("error reading migrations directory: %v", err)
	}

	// Sort migration files
	var migrationFiles []string
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".up.sql") {
			version := strings.TrimSuffix(file.Name(), ".up.sql")
			if !appliedMigrations[version] {
				migrationFiles = append(migrationFiles, file.Name())
			}
		}
	}

	if len(migrationFiles) == 0 {
		log.Println("migration state is up to date")
		return nil
	}

	// Sort migration files by name (which should be timestamp-based)
	sort.Strings(migrationFiles)

	// Apply each migration
	for _, filename := range migrationFiles {
		version := strings.TrimSuffix(filename, ".up.sql")
		log.Printf("applying migration: %s", version)

		// Read migration file
		content, err := os.ReadFile(filepath.Join(dir, filename))
		if err != nil {
			return fmt.Errorf("error reading migration file %s: %v", filename, err)
		}

		// Execute migration in a transaction
		tx, err := Conn.Begin()
		if err != nil {
			return fmt.Errorf("error starting transaction for migration %s: %v", version, err)
		}

		// Execute the migration SQL
		_, err = tx.Exec(string(content))
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error executing migration %s: %v", version, err)
		}

		// Record the migration as applied
		_, err = tx.Exec("INSERT INTO schema_migrations (version) VALUES ($1)", version)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("error recording migration %s: %v", version, err)
		}

		// Commit the transaction
		err = tx.Commit()
		if err != nil {
			return fmt.Errorf("error committing migration %s: %v", version, err)
		}

		log.Printf("migration %s applied successfully", version)
	}

	return nil
}
