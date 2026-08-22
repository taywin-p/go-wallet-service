package repository

import (
	"log"
	"time"

	"wallet-service/internal/config"
	"wallet-service/internal/domain"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Database struct {
	DB *gorm.DB
}

func NewDatabase(cfg config.Config) *Database {
	dsn := cfg.DSN()

	// Retry loop. Compose already gates startup on the db healthcheck, but this
	// also covers `go run` against a container that is still booting, and a
	// Postgres restart under an orchestrator.
	var db *gorm.DB
	var err error

	for i := 0; i < 5; i++ {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: logger.Default.LogMode(logLevel(cfg.DBLogLevel)),
		})
		if err == nil {
			break
		}
		log.Printf("Failed to connect to database. Retrying in 2 seconds... (%d/5)", i+1)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		log.Fatal("Failed to connect to database after retries: ", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal("Failed to access underlying sql.DB: ", err)
	}
	sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	log.Println("Connected to PostgreSQL database successfully")

	err = db.AutoMigrate(
		&domain.Wallet{},
		&domain.Transaction{},
	)
	if err != nil {
		log.Fatal("Failed to migrate database: ", err)
	}
	log.Println("Database migration completed")

	return &Database{DB: db}
}

func logLevel(name string) logger.LogLevel {
	switch name {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "info":
		return logger.Info
	default:
		return logger.Warn
	}
}
