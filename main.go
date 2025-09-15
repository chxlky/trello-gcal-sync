package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/chxlky/trello-gcal-sync/database"
	"github.com/chxlky/trello-gcal-sync/integrations"
	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Error loading .env file - relying on host environment variables")
	}

	viper.AutomaticEnv()

	trelloClient := integrations.NewTrelloClient(
		viper.GetString("TRELLO_API_KEY"),
		viper.GetString("TRELLO_API_TOKEN"),
		viper.GetString("TRELLO_CALLBACK_URL"),
	)

	log.Println("Registering Trello webhook...")

	boardId := viper.GetString("TRELLO_BOARD_ID")
	webhookID, err := trelloClient.RegisterWebhook(boardId)
	if err != nil {
		log.Fatalf("FATAL: Failed to register webhook on startup: %v", err)
	}
	log.Printf("Successfully registered webhook with ID: %s\n", webhookID)

	dbPath := viper.GetString("DB_PATH")
	if dbPath == "" {
		dbPath = "cards.db"
	}
	db := database.Init(dbPath)
	sqlDB, _ := db.DB()

	port := viper.GetString("SERVER_PORT")
	if port == "" {
		port = "8080"
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	done := make(chan struct{})
	var once sync.Once

	cleanup := func(reason string) {
		log.Printf("Shutdown initiated (%s). Beginning cleanup\n", reason)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		delErr := trelloClient.DeleteWebhook(webhookID)
		if delErr != nil {
			log.Printf("Error deleting webhook: %v\n", delErr)
		} else {
			log.Printf("Successfully deleted webhook with ID: %s\n", webhookID)
		}

		if sqlDB != nil {
			if err := sqlDB.Close(); err != nil {
				log.Printf("Error closing database: %v\n", err)
			} else {
				log.Println("Database connection closed.")
			}
		}

		select {
		case <-ctx.Done():
			// timeout or completed
		default:
		}
		close(done)
	}

	go func() {
		sig := <-sigCh
		once.Do(func() {
			cleanup(sig.String())
		})

		// if a second signal is caught, exit immediately
		go func() {
			<-sigCh
			log.Println("Second interrupt signal received. Exiting immediately.")
			os.Exit(1)
		}()
	}()

	log.Printf("Starting server on port %s...\n", port)

	<-done
	log.Println("Exiting...")
}
