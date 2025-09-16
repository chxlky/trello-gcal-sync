package database

import (
	"github.com/chxlky/trello-gcal-sync/internal/models"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func Init(dbPath string) *gorm.DB {
	dbFile := sqlite.Open(dbPath)
	db, err := gorm.Open(dbFile, &gorm.Config{})
	if err != nil {
		zap.L().Fatal("Failed to connect to database", zap.Error(err))
	}

	if err := db.AutoMigrate(&models.Card{}); err != nil {
		zap.L().Fatal("Failed to migrate database", zap.Error(err))
	}

	zap.L().Info("Database initialised and migrated successfully")

	return db
}
