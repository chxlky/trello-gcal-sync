package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/chxlky/trello-gcal-sync/integrations"
	"github.com/chxlky/trello-gcal-sync/internal/models"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Handler struct {
	DB        *gorm.DB
	CalClient *integrations.CalendarClient
}

func (h *Handler) TrelloWebhookHandler(c *gin.Context) {
	// Trello sends a HEAD request to validate the webhook endpoint upon creation
	if c.Request.Method != http.MethodPost {
		zap.L().Debug("Received non-POST request to webhook endpoint; responding with 200 OK")
		c.Status(http.StatusOK)
		return
	}

	var payload models.TrelloWebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		zap.L().Error("Could not bind JSON payload - likely an empty validation POST", zap.Error(err))
		// Respond with 200 OK to satisfy Trello's validation, even if the payload is empty
		c.Status(http.StatusOK)
		return
	}

	action := payload.Action
	card := action.Data.Card

	zap.L().Debug("Received Trello webhook", zap.String("actionType", action.Type), zap.String("cardID", card.ID))

	if err := h.processCardUpdate(payload); err != nil {
		zap.L().Error("Error processing card update", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process webhook"})
		return
	}

	zap.L().Info("Successfully processed card", zap.String("cardID", card.ID))
	c.JSON(http.StatusOK, gin.H{"message": "Event processed successfully"})
}

// processCardUpdate orchestrates the main sync logic for a card update
func (h *Handler) processCardUpdate(payload models.TrelloWebhookPayload) error {
	if payload.Action.Type != "updateCard" {
		zap.L().Debug("Action type is not 'updateCard', no action taken")
		return nil // Not an error, just nothing to do
	}

	incomingCardData := payload.Action.Data.Card

	if incomingCardData.ID == "" {
		zap.L().Debug("Incoming card data does not contain an ID, skipping sync")
		return nil
	}

	boardName := payload.Action.Data.Board.Name
	boardID := payload.Action.Data.Board.ID
	var card models.Card

	err := h.DB.First(&card, "id = ?", incomingCardData.ID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("database query failed: %w", err)
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		zap.L().Info("Card not found in database; creating new record", zap.String("cardID", incomingCardData.ID))
	}

	// Handle archiving
	wasArchived := card.Archived
	if incomingCardData.Closed {
		if !wasArchived {
			zap.L().Info("Card archived", zap.String("cardID", incomingCardData.ID), zap.String("cardName", incomingCardData.Name))
		}
		card.Archived = true

		if card.EventID != "" {
			if err := h.CalClient.DeleteEvent(card.EventID); err != nil {
				zap.L().Warn("Failed to delete event from Google Calendar for archived card", zap.String("eventID", card.EventID), zap.Error(err))
			}
			// Clear the event ID since it's deleted
			card.EventID = ""
		}
	} else {
		if wasArchived {
			zap.L().Info("Card unarchived", zap.String("cardID", incomingCardData.ID), zap.String("cardName", incomingCardData.Name))
		}
		card.Archived = false
	}

	// Skip sync for archived cards
	if card.Archived {
		zap.L().Info("Skipping further sync for archived card", zap.String("cardID", incomingCardData.ID))
	} else {
		// Decide whether to sync an event or delete one based on the due date
		if incomingCardData.Due != "" {
			if err := h.syncCalendarEvent(&card, incomingCardData, boardName, boardID); err != nil {
				return err
			}
		} else {
			if card.DueDate != nil && card.EventID == "" {
				// Recreate event using DB due date
				zap.L().Info("Card has due date in DB but no event, recreating event", zap.String("cardID", card.ID))
				// Create a copy of incoming with the DB due date
				recreateIncoming := incomingCardData
				recreateIncoming.Due = card.DueDate.Format(time.RFC3339)
				if err := h.syncCalendarEvent(&card, recreateIncoming, boardName, boardID); err != nil {
					return err
				}
			} else if card.DueDate != nil && card.EventID != "" {
				zap.L().Info("Card has due date in DB, keeping existing event", zap.String("cardID", card.ID))
			} else {
				if err := h.deleteCalendarEvent(&card); err != nil {
					return err
				}
			}
		}
	}

	if err := h.DB.Save(&card).Error; err != nil {
		return fmt.Errorf("failed to save final card state: %w", err)
	}

	return nil
}

func (h *Handler) syncCalendarEvent(card *models.Card, incoming models.TrelloCardData, boardName string, boardID string) error {
	if card.Archived {
		zap.L().Info("Skipping event sync for archived card", zap.String("cardID", card.ID))
		return nil
	}

	newDueDate, err := time.Parse(time.RFC3339, incoming.Due)
	if err != nil {
		return fmt.Errorf("invalid due date format: %w", err)
	}

	var boardPrefix string
	if boardName != "" {
		runes := []rune(boardName)
		boardPrefix = string(runes[0])
	} else {
		boardPrefix = ""
	}
	prefixedName := fmt.Sprintf("[%s] %s", boardPrefix, incoming.Name)

	// Update card details from the incoming payload
	card.ID = incoming.ID
	card.Name = prefixedName
	card.DueDate = &newDueDate
	card.URL = fmt.Sprintf("https://trello.com/c/%s", incoming.ShortLink)
	card.BoardID = boardID

	if card.EventID != "" {
		// Update existing event
		zap.L().Info("Due date updated for card; updating associated event", zap.String("cardID", card.ID), zap.String("eventID", card.EventID))
		updatedEvent, err := h.CalClient.UpdateEvent(*card, card.EventID)
		if err != nil {
			return fmt.Errorf("failed to update event in Google Calendar: %w", err)
		}
		zap.L().Info("Successfully updated event for card", zap.String("eventID", updatedEvent.Id), zap.String("cardID", card.ID))
		card.EventID = updatedEvent.Id
	} else {
		// Create new event
		zap.L().Info("Due date set for card; creating new event in Google Calendar", zap.String("cardID", card.ID))
		createdEvent, err := h.CalClient.CreateEvent(*card)
		if err != nil {
			return fmt.Errorf("failed to create event in Google Calendar: %w", err)
		}
		zap.L().Info("Successfully created event for card", zap.String("eventID", createdEvent.Id), zap.String("cardID", card.ID))
		card.EventID = createdEvent.Id
	}
	return nil
}

func (h *Handler) deleteCalendarEvent(card *models.Card) error {
	if card.EventID == "" {
		zap.L().Info("Due date removed for card but no associated event found to delete", zap.String("cardID", card.ID))
		return nil // Nothing to do
	}

	zap.L().Info("Due date removed for card; deleting associated event", zap.String("cardID", card.ID), zap.String("eventID", card.EventID))
	if err := h.CalClient.DeleteEvent(card.EventID); err != nil {
		// Log the error but don't block saving the state, as the event might already be gone
		zap.L().Warn("Failed to delete event from Google Calendar", zap.String("eventID", card.EventID), zap.Error(err))
	}

	// Clear local record of the event
	card.EventID = ""
	card.DueDate = nil
	return nil
}

func (h *Handler) HealthCheckHandler(c *gin.Context) {
	// Check database connectivity
	if err := h.DB.Exec("SELECT 1").Error; err != nil {
		zap.L().Error("Health check failed: database not reachable", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": "database"})
	}

	// Check Google Calendar client
	if h.CalClient == nil {
		zap.L().Error("Health check failed: Google Calendar client not initialised")
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": "google calendar client"})
		return
	}

	zap.L().Debug("Health check passed")
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}