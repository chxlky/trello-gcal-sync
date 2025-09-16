package api

import (
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
	// Trello can send HEAD, GET, and POST requests to the webhook URL
	if c.Request.Method != http.MethodPost {
		log.Println("Received non-POST request to webhook endpoint; responding with 200 OK")
		c.Status(http.StatusOK)
		return
	}

	// From now on, assume it's a POST request with JSON payload
	var payload models.TrelloWebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		// This could happen if Trello sends an empty POST request to verify the webhook
		log.Printf("Could not bind JSON payload - likely empty POST request: %v\n", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	action := payload.Action
	incomingCardData := action.Data.Card
	log.Printf("Received Trello webhook: action type=%s, card ID=%s\n", action.Type, incomingCardData.ID)

	if action.Type != "updateCard" {
		c.JSON(http.StatusOK, gin.H{"message": "No action taken"})
		return
	}

	var existingCard models.Card
	err := h.DB.First(&existingCard, "id = ?", incomingCardData.ID).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		log.Printf("Error querying database: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database query failed"})
		return
	}

	if incomingCardData.Due == "" {
		// Due date was removed - delete event
		if existingCard.EventID != "" {
			log.Printf("Due date removed for card %s; deleting associated event %s\n", existingCard.ID, existingCard.EventID)
			if err := h.CalClient.DeleteEvent(existingCard.EventID); err != nil {
				log.Printf("Error deleting event from Google Calendar: %v\n", err)
			}
			existingCard.EventID = ""
		}

		existingCard.DueDate = nil
		existingCard.Name = incomingCardData.Name
		existingCard.URL = fmt.Sprintf("https://trello.com/c/%s", incomingCardData.ShortLink)
		if existingCard.ID == "" {
			existingCard.ID = incomingCardData.ID
		}

	} else {
		// Due date was added or changed - create or update event
		newDueDate, parseErr := time.Parse(time.RFC3339, incomingCardData.Due)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid due date format"})
			return
		}

		cardToSync := models.Card{
			ID:      incomingCardData.ID,
			Name:    incomingCardData.Name,
			DueDate: &newDueDate,
			URL:     fmt.Sprintf("https://trello.com/c/%s", incomingCardData.ShortLink),
		}

		if existingCard.EventID != "" {
			// Update existing event
			log.Printf("Due date updated for card %s; updating associated event %s\n", cardToSync.ID, existingCard.EventID)
			updatedEvent, err := h.CalClient.UpdateEvent(cardToSync, existingCard.EventID)
			if err != nil {
				log.Printf("Error updating event in Google Calendar: %v\n", err)
			} else {
				log.Printf("Successfully updated event %s for card %s\n", updatedEvent.Id, cardToSync.ID)
				cardToSync.EventID = updatedEvent.Id
			}
		} else {
			// Create new event
			log.Printf("Due date set for card %s; creating new event in Google Calendar\n", cardToSync.ID)
			createdEvent, err := h.CalClient.CreateEvent(cardToSync)
			if err != nil {
				log.Printf("Error creating event in Google Calendar: %v\n", err)
			} else {
				log.Printf("Successfully created event %s for card %s\n", createdEvent.Id, cardToSync.ID)
				cardToSync.EventID = createdEvent.Id
			}
		}

		// Update local card record
		existingCard = cardToSync
	}

	// Save all changes back to our database.
	if err := h.DB.Save(&existingCard).Error; err != nil {
		log.Printf("Error saving final card state to database: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save card"})
		return
	}

	log.Printf("Successfully processed card %s.", existingCard.ID)
	c.JSON(http.StatusOK, gin.H{"message": "Event processed successfully"})
}
