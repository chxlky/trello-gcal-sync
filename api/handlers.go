package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/chxlky/trello-gcal-sync/integrations"
	"github.com/chxlky/trello-gcal-sync/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	DB        *gorm.DB
	CalClient *integrations.CalendarClient
}

func (h *Handler) TrelloWebhookHandler(c *gin.Context) {
	// Trello sends a HEAD request to validate the webhook endpoint upon creation
	if c.Request.Method != http.MethodPost {
		log.Println("Received non-POST request to webhook endpoint; responding with 200 OK")
		c.Status(http.StatusOK)
		return
	}

	var payload models.TrelloWebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		log.Printf("Could not bind JSON payload - likely an empty validation POST: %v\n", err)
		// Respond with 200 OK to satisfy Trello's validation, even if the payload is empty
		c.Status(http.StatusOK)
		return
	}

	action := payload.Action
	card := action.Data.Card

	log.Printf("Received Trello webhook: action type=%s, card ID=%s\n", action.Type, card.ID)

	if err := h.processCardUpdate(payload); err != nil {
		log.Printf("Error processing card update: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process webhook"})
		return
	}

	log.Printf("Successfully processed card %s", card.ID)
	c.JSON(http.StatusOK, gin.H{"message": "Event processed successfully"})
}

// processCardUpdate orchestrates the main sync logic for a card update
func (h *Handler) processCardUpdate(payload models.TrelloWebhookPayload) error {
	if payload.Action.Type != "updateCard" {
		log.Println("Action type is not 'updateCard', no action taken")
		return nil // Not an error, just nothing to do
	}

	incomingCardData := payload.Action.Data.Card

	if incomingCardData.ID == "" {
		log.Printf("incoming card data does not contain an ID, skipping sync\n")
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
		log.Printf("Card %s not found in database; creating new record\n", incomingCardData.ID)
	}

	// Handle archiving
	wasArchived := card.Archived
	if incomingCardData.Closed {
		if !wasArchived {
			log.Printf("Card %s (%s) has been archived\n", incomingCardData.ID, incomingCardData.Name)
		}
		card.Archived = true

		if card.EventID != "" {
			if err := h.CalClient.DeleteEvent(card.EventID); err != nil {
				log.Printf("Warning: failed to delete event from Google Calendar for archived card: %v\n", err)
			}
			// Clear the event ID since it's deleted
			card.EventID = ""
		}
	} else {
		if wasArchived {
			log.Printf("Card %s (%s) has been unarchived\n", incomingCardData.ID, incomingCardData.Name)
		}
		card.Archived = false
	}

	// Skip sync for archived cards
	if card.Archived {
		log.Printf("Skipping further sync for archived card %s\n", incomingCardData.ID)
	} else {
		// Decide whether to sync an event or delete one based on the due date
		if incomingCardData.Due != "" {
			if err := h.syncCalendarEvent(&card, incomingCardData, boardName, boardID); err != nil {
				return err
			}
		} else {
			if card.DueDate != nil && card.EventID == "" {
				// Recreate event using DB due date
				log.Printf("Card %s has due date in DB but no event, recreating event\n", card.ID)
				// Create a copy of incoming with the DB due date
				recreateIncoming := incomingCardData
				recreateIncoming.Due = card.DueDate.Format(time.RFC3339)
				if err := h.syncCalendarEvent(&card, recreateIncoming, boardName, boardID); err != nil {
					return err
				}
			} else if card.DueDate != nil && card.EventID != "" {
				log.Printf("Card %s has due date in DB, keeping existing event\n", card.ID)
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
		log.Printf("Skipping event sync for archived card %s\n", card.ID)
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
		log.Printf("Due date updated for card %s; updating associated event %s\n", card.ID, card.EventID)
		updatedEvent, err := h.CalClient.UpdateEvent(*card, card.EventID)
		if err != nil {
			return fmt.Errorf("failed to update event in Google Calendar: %w", err)
		}
		log.Printf("Successfully updated event %s for card %s\n", updatedEvent.Id, card.ID)
		card.EventID = updatedEvent.Id
	} else {
		// Create new event
		log.Printf("Due date set for card %s; creating new event in Google Calendar\n", card.ID)
		createdEvent, err := h.CalClient.CreateEvent(*card)
		if err != nil {
			return fmt.Errorf("failed to create event in Google Calendar: %w", err)
		}
		log.Printf("Successfully created event %s for card %s\n", createdEvent.Id, card.ID)
		card.EventID = createdEvent.Id
	}
	return nil
}

func (h *Handler) deleteCalendarEvent(card *models.Card) error {
	if card.EventID == "" {
		log.Printf("Due date removed for card %s, but no associated event found to delete\n", card.ID)
		return nil // Nothing to do
	}

	log.Printf("Due date removed for card %s; deleting associated event %s\n", card.ID, card.EventID)
	if err := h.CalClient.DeleteEvent(card.EventID); err != nil {
		// Log the error but don't block saving the state, as the event might already be gone
		log.Printf("Warning: failed to delete event from Google Calendar: %v\n", err)
	}

	// Clear local record of the event
	card.EventID = ""
	card.DueDate = nil
	return nil
}
