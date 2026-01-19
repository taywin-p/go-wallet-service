package repository

import (
	"log"
	"time"

	"wallet-service/internal/domain"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Database struct {
	DB *gorm.DB
}

func NewDatabase() *Database {
	dsn := "host=localhost user=user password=password dbname=wallet_db port=5432 sslmode=disable TimeZone=Asia/Bangkok"
	
	// Retry connection loop
	var db *gorm.DB
	var err error
	
	for i := 0; i < 5; i++ {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Info),
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

	log.Println("Connected to PostgreSQL database successfully")

	// Auto Migrate
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
