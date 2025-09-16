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
	cardData := action.Data.Card
	log.Printf("Received Trello webhook: action type=%s, card ID=%s\n", action.Type, cardData.ID)

	if action.Type == "updateCard" && cardData.Due != "" {
		dueDate, err := time.Parse(time.RFC3339, cardData.Due)
		if err != nil {
			log.Printf("Error parsing due date '%s': %v\n", cardData.Due, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid due date format"})
			return
		}

		card := models.Card{
			ID:      cardData.ID,
			Name:    cardData.Name,
			DueDate: &dueDate,
			URL:     fmt.Sprintf("https://trello.com/c/%s", cardData.ShortLink),
		}

		if result := h.DB.Save(&card); result.Error != nil {
			log.Printf("Error saving card to database: %v\n", result.Error)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save card"})
			return
		}
		log.Printf("Card saved/updated: ID=%s, Name=%s, DueDate=%s\n", card.ID, card.Name, card.DueDate)

		event, err := h.CalClient.CreateEventFromCard(card)
		if err != nil {
			log.Printf("Error creating calendar event from card: %v\n", err)
		} else {
			log.Printf("Created calendar event: ID=%s, Summary=%s, Link=%s\n", event.Id, event.Summary, event.HtmlLink)
		}

		c.JSON(http.StatusOK, gin.H{"message": "Card saved/updated successfully"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "No action taken"})
}
