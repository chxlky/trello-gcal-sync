package models

import "time"

type Card struct {
	ID        string `gorm:"primaryKey"`
	Name      string
	DueDate   *time.Time
	URL       string
	BoardID   string
	CreatedAt time.Time
	UpdatedAt time.Time
	EventID   string // Google Calendar Event ID
}
