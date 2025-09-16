package integrations

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chxlky/trello-gcal-sync/internal/models"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type CalendarClient struct {
	service *calendar.Service
}

func NewCalendarClient() (*CalendarClient, error) {
	ctx := context.Background()

	settings := viper.Get("google.service_account")

	jsonBytes, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal service account settings to JSON: %w", err)
	}

	// create credentials from JSON data
	config, err := google.JWTConfigFromJSON(jsonBytes, calendar.CalendarScope)
	if err != nil {
		return nil, fmt.Errorf("unable to parse service account credentials from JSON: %w", err)
	}

	client := config.Client(ctx)

	srv, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve Calendar client: %w", err)
	}

	return &CalendarClient{service: srv}, nil
}

func (c *CalendarClient) CreateEvent(card models.Card) (*calendar.Event, error) {
	if card.DueDate == nil {
		return nil, fmt.Errorf("card does not have a due date, cannot create event")
	}

	calendarID := viper.GetString("google.calendar.calendar_id")
	if calendarID == "" {
		return nil, fmt.Errorf("google calendar ID is not configured")
	}

	event := &calendar.Event{
		Summary:     card.Name,
		Description: fmt.Sprintf("Trello Card: %s", card.URL),
		Start: &calendar.EventDateTime{
			Date: card.DueDate.Format("2006-01-02"),
		},
		End: &calendar.EventDateTime{
			Date: card.DueDate.AddDate(0, 0, 1).Format("2006-01-02"), // all-day event ends the next day
		},
	}

	createdEvent, err := c.service.Events.Insert(calendarID, event).Do()
	if err != nil {
		return nil, fmt.Errorf("unable to create event in Google Calendar: %w", err)
	}

	return createdEvent, nil
}

func (c *CalendarClient) UpdateEvent(card models.Card, eventID string) (*calendar.Event, error) {
	if card.DueDate == nil {
		return nil, fmt.Errorf("card does not have a due date, cannot update event")
	}

	calendarID := viper.GetString("google.calendar.calendar_id")
	if calendarID == "" {
		return nil, fmt.Errorf("google calendar ID is not configured")
	}

	event, err := c.service.Events.Get(calendarID, eventID).Do()
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve event from Google Calendar: %w", err)
	}

	event.Summary = card.Name
	event.Description = fmt.Sprintf("Trello Card: %s", card.URL)
	event.Start = &calendar.EventDateTime{
		Date: card.DueDate.Format("2006-01-02"),
	}
	event.End = &calendar.EventDateTime{
		Date: card.DueDate.AddDate(0, 0, 1).Format("2006-01-02"), // all-day event ends the next day
	}

	updatedEvent, err := c.service.Events.Update(calendarID, event.Id, event).Do()
	if err != nil {
		return nil, fmt.Errorf("unable to update event in Google Calendar: %w", err)
	}

	return updatedEvent, nil
}

func (c *CalendarClient) DeleteEvent(eventID string) error {
	calendarID := viper.GetString("google.calendar.calendar_id")
	if calendarID == "" {
		return fmt.Errorf("google calendar ID is not configured")
	}

	err := c.service.Events.Delete(calendarID, eventID).Do()
	if err != nil {
		// It's possible the event was already deleted, so we can choose to ignore "Not Found" errors
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
			zap.L().Info("Event not found in Google Calendar. Already deleted.", zap.String("eventID", eventID))
			return nil
		}
		return fmt.Errorf("unable to delete event from Google Calendar: %w", err)
	}

	return nil
}
