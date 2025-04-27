package models

import "time"

type Client struct {
	ID           int
	ClientID    string
	Capacity     int
	RatePerSec int
	CreatedAt   time.Time
}
