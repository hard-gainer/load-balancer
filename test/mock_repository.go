package test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hard-gainer/load-balancer/internal/models"
	"github.com/hard-gainer/load-balancer/internal/storage"
)

var (
	ErrClientNotFound      = errors.New("client not found")
	ErrClientAlreadyExists = errors.New("client already exists")
)

// InMemoryRepository implements the Repository interface for the testing purpose 
type InMemoryRepository struct {
	clients map[string]models.Client
	mu      sync.RWMutex
}

// NewInMemoryRepository creates in-memory repository for the testing purpose
func NewInMemoryRepository() storage.Repository {
	return &InMemoryRepository{
		clients: make(map[string]models.Client),
	}
}

// GetAllClients mocks the repository's GetAllClients method
func (r *InMemoryRepository) GetAllClients(ctx context.Context) ([]models.Client, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]models.Client, 0, len(r.clients))
	for _, client := range r.clients {
		result = append(result, client)
	}
	return result, nil
}

// SaveClient mocks the repository's SaveClient method
func (r *InMemoryRepository) SaveClient(ctx context.Context, client models.Client) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.clients[client.ClientID]; exists {
		return ErrClientAlreadyExists
	}

	if client.ID == 0 {
		client.ID = int(len(r.clients) + 1)
	}
	if client.CreatedAt.IsZero() {
		client.CreatedAt = time.Now()
	}

	r.clients[client.ClientID] = client
	return nil
}

// UpdateClient mocks the repository's UpdateClient method
func (r *InMemoryRepository) UpdateClient(ctx context.Context, client models.Client) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.clients[client.ClientID]
	if !exists {
		return ErrClientNotFound
	}

	client.ID = existing.ID
	client.CreatedAt = existing.CreatedAt

	r.clients[client.ClientID] = client
	return nil
}

// RemoveClient mocks the repository's RemoveClient method
func (r *InMemoryRepository) RemoveClient(ctx context.Context, clientID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.clients[clientID]; !exists {
		return ErrClientNotFound
	}

	delete(r.clients, clientID)
	return nil
}

// Close mocks the repository's Close method
func (r *InMemoryRepository) Close() {
}
