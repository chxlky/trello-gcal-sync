package database

import (
	"log"

	"github.com/chxlky/trello-gcal-sync/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func Init(dbPath string) *gorm.DB {
	dbFile := sqlite.Open(dbPath)
	db, err := gorm.Open(dbFile, &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	if err := db.AutoMigrate(&models.Card{}); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	log.Println("Database initialised and migrated successfully")

	return db
}