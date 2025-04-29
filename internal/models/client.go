package models

import "time"

// Client is a structure for a clients
type Client struct {
	ID           int
	ClientID    string
	Capacity     int
	RatePerSec int
	CreatedAt   time.Time
}
