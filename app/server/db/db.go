package db

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
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
	return nil
}
