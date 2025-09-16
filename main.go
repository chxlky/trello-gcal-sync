package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/chxlky/trello-gcal-sync/api"
	"github.com/chxlky/trello-gcal-sync/database"
	"github.com/chxlky/trello-gcal-sync/integrations"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func main() {
	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("FATAL: Error reading config file: %v", err)
	}

	dbPath := viper.GetString("database.path")
	if dbPath == "" {
		dbPath = "cards.db"
	}
	db := database.Init(dbPath)
	sqlDB, _ := db.DB()

	port := viper.GetString("server.port")
	if port == "" {
		port = "8080"
	}

	calClient, err := integrations.NewCalendarClient()
	if err != nil {
		log.Fatalf("FATAL: Failed to initialize Google Calendar client: %v", err)
	}
	log.Println("Successfully authenticated with Google Calendar API.")

	router := gin.Default()
	apiHandler := &api.Handler{
		DB:        db,
		CalClient: calClient,
	}
	apiGroup := router.Group("/api")
	{
		apiGroup.POST("/trello-webhook", apiHandler.TrelloWebhookHandler)
		apiGroup.HEAD("/trello-webhook", apiHandler.TrelloWebhookHandler)
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	log.Printf("Starting server on port %s...\n", port)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("FATAL: Server error: %v", err)
		}
	}()

	// Give the server a moment to start
	time.Sleep(250 * time.Millisecond)

	trelloClient := integrations.NewTrelloClient(
		viper.GetString("trello.api_key"),
		viper.GetString("trello.api_token"),
		viper.GetString("trello.callback_url"),
	)

	log.Println("Registering Trello webhook...")

	boardId := viper.GetString("trello.board_id")
	webhookID, err := trelloClient.RegisterWebhook(boardId)
	if err != nil {
		log.Fatalf("FATAL: Failed to register webhook on startup: %v", err)
	}
	log.Printf("Successfully registered webhook with ID: %s\n", webhookID)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	done := make(chan struct{})
	var once sync.Once

	cleanup := func(reason string) {
		log.Printf("Shutdown initiated (%s). Beginning cleanup\n", reason)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		log.Println("Shutting down HTTP server...")
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down server: %v\n", err)
		} else {
			log.Println("HTTP server shut down gracefully.")
		}

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

	<-done
	log.Println("Exiting...")
}
