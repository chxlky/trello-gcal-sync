package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chxlky/trello-gcal-sync/api"
	"github.com/chxlky/trello-gcal-sync/database"
	"github.com/chxlky/trello-gcal-sync/integrations"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	levelStr := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if levelStr == "" {
		levelStr = "debug"
	}
	level, err := zapcore.ParseLevel(levelStr)
	if err != nil {
		level = zapcore.InfoLevel
	}

	encoderConfig := zap.NewDevelopmentEncoderConfig()
	encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Development:      true,
		Encoding:         "console",
		EncoderConfig:    encoderConfig,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, _ := config.Build()
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err != nil {
		zap.L().Fatal("Error reading config file", zap.Error(err))
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
		zap.L().Fatal("Failed to initialise Google Calendar client", zap.Error(err))
	}
	zap.L().Info("Successfully authenticated with Google Calendar API.")

	router := gin.Default()
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))

	apiHandler := &api.Handler{
		DB:        db,
		CalClient: calClient,
		Workers:   make(chan struct{}, 10), // Limit to 10 concurrent workers
	}
	apiGroup := router.Group("/api")
	{
		apiGroup.POST("/trello-webhook", apiHandler.TrelloWebhookHandler)
		apiGroup.HEAD("/trello-webhook", apiHandler.TrelloWebhookHandler)
		apiGroup.GET("/health", apiHandler.HealthCheckHandler)
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	zap.L().Info("Starting server", zap.String("port", port))
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			zap.L().Fatal("Server error", zap.Error(err))
		}
	}()

	// Give the server a moment to start
	time.Sleep(250 * time.Millisecond)

	trelloClient := integrations.NewTrelloClient(
		viper.GetString("trello.api_key"),
		viper.GetString("trello.api_token"),
		viper.GetString("trello.callback_url"),
	)

	var boardIDs []string
	if err := viper.UnmarshalKey("trello.board_ids", &boardIDs); err != nil || len(boardIDs) == 0 {
		zap.L().Fatal("trello.board_ids is not configured properly", zap.Error(err))
	}

	zap.L().Info("Registering Trello webhook for boards", zap.Strings("boardIDs", boardIDs))

	webhookIDs := make(map[string]string)
	for _, boardId := range boardIDs {
		webhookID, err := trelloClient.RegisterWebhook(boardId)
		if err != nil {
			zap.L().Fatal("Failed to register webhook on startup for board", zap.String("boardID", boardId), zap.Error(err))
		}
		webhookIDs[boardId] = webhookID
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	done := make(chan struct{})
	var once sync.Once

	cleanup := func(reason string) {
		zap.L().Info("Shutdown initiated", zap.String("reason", reason))

		close(apiHandler.Workers) // Close the channel to stop accepting new work

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		zap.L().Info("Shutting down HTTP server...")
		if err := srv.Shutdown(ctx); err != nil {
			zap.L().Error("Error shutting down server", zap.Error(err))
		} else {
			zap.L().Info("HTTP server shut down gracefully.")
		}

		for boardID, webhookID := range webhookIDs {
			if err := trelloClient.DeleteWebhook(webhookID); err != nil {
				zap.L().Error("Error deleting webhook for board", zap.String("boardID", boardID), zap.Error(err))
			} else {
				zap.L().Info("Successfully deleted webhook for board", zap.String("boardID", boardID))
			}
		}

		if sqlDB != nil {
			if err := sqlDB.Close(); err != nil {
				zap.L().Error("Error closing database", zap.Error(err))
			} else {
				zap.L().Info("Database connection closed.")
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
			zap.L().Info("Second interrupt signal received. Exiting immediately.")
			os.Exit(1)
		}()
	}()

	<-done
	zap.L().Info("Exiting...")
}
