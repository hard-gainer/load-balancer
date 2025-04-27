package storage

import "errors"

var (
	// ErrClientAlreadyExists is returned when trying to create a client that already exists
	ErrClientAlreadyExists = errors.New("such client already exists")
	// ErrClientNotFound is returned when a client doesn't exist
	ErrClientNotFound      = errors.New("client not found")
)
