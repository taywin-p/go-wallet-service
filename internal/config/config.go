// Package config loads application settings from environment variables.
//
// Every setting has a default that matches the docker-compose Postgres service,
// so `go run ./cmd/api` works with no environment set up at all, while the
// container overrides DB_HOST (and friends) to reach the db service by name.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string
	DBTimeZone string
	DBLogLevel string

	// Connection pool. Left unbounded, GORM will happily open more connections
	// than Postgres' max_connections allows.
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	// Server-side timeouts, applied per connection via the DSN options string.
	// StatementTimeout and LockTimeout stop a query from waiting on a row lock
	// forever; IdleInTxTimeout reaps transactions whose client died mid-flight
	// while still holding FOR UPDATE locks.
	StatementTimeout time.Duration
	LockTimeout      time.Duration
	IdleInTxTimeout  time.Duration

	AppPort string
}

func Load() Config {
	return Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "user"),
		DBPassword: getEnv("DB_PASSWORD", "password"),
		DBName:     getEnv("DB_NAME", "wallet_db"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),
		DBTimeZone: getEnv("DB_TIMEZONE", "Asia/Bangkok"),
		// warn by default: at info GORM logs every statement, and those
		// statements carry account balances.
		DBLogLevel: strings.ToLower(getEnv("DB_LOG_LEVEL", "warn")),

		DBMaxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", 10),
		DBConnMaxLifetime: time.Duration(getEnvInt("DB_CONN_MAX_LIFETIME_MINUTES", 60)) * time.Minute,

		StatementTimeout: time.Duration(getEnvInt("DB_STATEMENT_TIMEOUT_MS", 5000)) * time.Millisecond,
		LockTimeout:      time.Duration(getEnvInt("DB_LOCK_TIMEOUT_MS", 3000)) * time.Millisecond,
		IdleInTxTimeout:  time.Duration(getEnvInt("DB_IDLE_IN_TX_TIMEOUT_MS", 10000)) * time.Millisecond,

		AppPort: getEnv("APP_PORT", "8080"),
	}
}

// DSN builds the Postgres connection string. The options field carries the
// per-session timeouts; pgx passes it through to the server as startup options.
func (c Config) DSN() string {
	options := fmt.Sprintf("-c statement_timeout=%d -c lock_timeout=%d -c idle_in_transaction_session_timeout=%d",
		c.StatementTimeout.Milliseconds(),
		c.LockTimeout.Milliseconds(),
		c.IdleInTxTimeout.Milliseconds(),
	)

	return fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=%s options='%s'",
		c.DBHost, c.DBUser, c.DBPassword, c.DBName, c.DBPort, c.DBSSLMode, c.DBTimeZone, options,
	)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
