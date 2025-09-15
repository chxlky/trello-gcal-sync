package main

import (
	"log"

	"github.com/chxlky/trello-gcal-sync/database"
	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Error loading .env file - relying on host environment variables")
	}

	viper.AutomaticEnv()

	dbPath := viper.GetString("DB_PATH")
	if dbPath == "" {
		dbPath = "cards.db"
	}
	db := database.Init(dbPath)
	_ = db // Placeholder to avoid unused variable error

	port := viper.GetString("SERVER_PORT")
	if port == "" {
		port = "8080"
	}

}
