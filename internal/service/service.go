package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/hard-gainer/load-balancer/internal/config"
	"github.com/hard-gainer/load-balancer/internal/models"
	"github.com/hard-gainer/load-balancer/internal/storage"
)

// TokenBucket is a structure for a rate-limiting alogorithm
type TokenBucket struct {
	Capacity   int
	Tokens     int
	RatePerSec int
	LastRefill time.Time
	mu         sync.Mutex
}

// LoadBalancerService is service interface
type LoadBalancerService interface {
	FindAllClients(ctx context.Context, backends []models.Backend) ([]models.Client, error)
	CreateClient(ctx context.Context, client models.Client) error
	UpdateClient(ctx context.Context, client models.Client) error
	DeleteClient(ctx context.Context, id string) error
	IsRequestAllowed(ctx context.Context, clientID string) (bool, error)
	CountRequest(ctx context.Context, clientID string) (bool, error)
	Shutdown()
}

// LoadBalancerServiceImpl is an implementation of LoadBalancerService
type LoadBalancerServiceImpl struct {
	storage           storage.Repository
	rateLimiter       map[string]*TokenBucket
	mu                sync.RWMutex
	ticker            *time.Ticker
	done              chan struct{}
	defaultCapacity   int
	defaultRatePerSec int
}

// NewLoadBalancerService returns a new LoadBalancerService and
// starts a goroutine with refillTokens()
func NewLoadBalancerService(
	storage storage.Repository,
	cfg *config.Config,
) (LoadBalancerService, error) {
	service := &LoadBalancerServiceImpl{
		storage:           storage,
		rateLimiter:       make(map[string]*TokenBucket),
		ticker:            time.NewTicker(time.Second),
		done:              make(chan struct{}),
		defaultCapacity:   cfg.ClientDefaultVals.Capacity,
		defaultRatePerSec: cfg.ClientDefaultVals.RatePerSec,
	}

	if service.defaultCapacity <= 0 {
		service.defaultCapacity = 10
		slog.Warn("invalid default capacity in config, using fallback value",
			"value", 10)
	}
	if service.defaultRatePerSec <= 0 {
		service.defaultRatePerSec = 1
		slog.Warn("invalid default rate_per_sec in config, using fallback value",
			"value", 1)
	}

	slog.Info("loaded client configuration",
		"capacity", service.defaultCapacity,
		"rate_per_sec", service.defaultRatePerSec)

	if err := service.InitRateLimiter(context.Background()); err != nil {
		return nil, err
	}

	go service.refillTokens()

	return service, nil
}

// InitRateLimiter loads all clients from a database to add them into the rateLimiter
func (s *LoadBalancerServiceImpl) InitRateLimiter(ctx context.Context) error {
	clients, err := s.storage.GetAllClients(ctx)
	if err != nil {
		slog.Error("failed to get clients", "error", err)
		return err
	}

	for _, client := range clients {
		s.rateLimiter[client.ClientID] = &TokenBucket{
			Capacity:   client.Capacity,
			Tokens:     client.Capacity,
			RatePerSec: client.RatePerSec,
			LastRefill: time.Now(),
		}
	}

	return nil
}

// refillTokens pereodically refills tokens for all clients
func (s *LoadBalancerServiceImpl) refillTokens() {
	for {
		select {
		case <-s.ticker.C:
			s.mu.RLock()
			for _, bucket := range s.rateLimiter {
				s.refillBucket(bucket)
			}
			s.mu.RUnlock()
		case <-s.done:
			s.ticker.Stop()
			return
		}
	}
}

// refillBucket refills tokens for a certain client
func (s *LoadBalancerServiceImpl) refillBucket(bucket *TokenBucket) {
	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	now := time.Now()
	elapsedSeconds := now.Sub(bucket.LastRefill).Seconds()
	tokensToAdd := int(elapsedSeconds * float64(bucket.RatePerSec))

	if tokensToAdd > 0 {
		bucket.Tokens += tokensToAdd
		if bucket.Tokens > bucket.Capacity {
			bucket.Tokens = bucket.Capacity
		}
		bucket.LastRefill = now
	}
}

// FindAllClients returns all clients
func (s *LoadBalancerServiceImpl) FindAllClients(
	ctx context.Context,
	backends []models.Backend,
) ([]models.Client, error) {
	slog.Info("finding all clients", "backends_count", len(backends))

	clients, err := s.storage.GetAllClients(ctx)
	if err != nil {
		slog.Error("failed to get clients from storage", "error", err)
		return nil, err
	}

	return clients, nil
}

// FindAllClients saves a new client
func (s *LoadBalancerServiceImpl) CreateClient(
	ctx context.Context,
	client models.Client,
) error {
	clients, err := s.storage.GetAllClients(ctx)
	if err != nil {
		slog.Error("failed to get clients for duplication check", "error", err)
		return err
	}

	for _, existingClient := range clients {
		if existingClient.ClientID == client.ClientID {
			slog.Warn("client already exists", "client_id", client.ClientID)
			return ErrClientAlreadyExists
		}
	}

	if err := s.storage.SaveClient(ctx, client); err != nil {
		slog.Error("failed to save client", "error", err, "client_id", client.ClientID)
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.rateLimiter[client.ClientID] = &TokenBucket{
		Capacity:   client.Capacity,
		Tokens:     client.Capacity,
		RatePerSec: client.RatePerSec,
		LastRefill: time.Now(),
	}

	slog.Info("client created successfully", "client_id", client.ClientID)
	return nil
}

// UpdateClient updates the client fields
func (s *LoadBalancerServiceImpl) UpdateClient(
	ctx context.Context,
	client models.Client,
) error {
	if err := s.storage.UpdateClient(ctx, client); err != nil {
		if errors.Is(err, ErrClientNotFound) {
			slog.Warn("client not found during update", "client_id", client.ClientID)
			return ErrClientNotFound
		}
		slog.Error("failed to update client", "error", err, "client_id", client.ClientID)
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	bucket, exists := s.rateLimiter[client.ClientID]
	if !exists {
		s.rateLimiter[client.ClientID] = &TokenBucket{
			Capacity:   client.Capacity,
			Tokens:     client.Capacity,
			RatePerSec: client.RatePerSec,
			LastRefill: time.Now(),
		}
	} else {
		bucket.mu.Lock()
		bucket.Capacity = client.Capacity

		if bucket.Tokens > client.Capacity {
			bucket.Tokens = client.Capacity
		}
		bucket.RatePerSec = client.RatePerSec
		bucket.mu.Unlock()
	}

	slog.Info("client updated successfully", "client_id", client.ClientID)
	return nil
}

// DeleteClientA deletes the certain client
func (s *LoadBalancerServiceImpl) DeleteClient(
	ctx context.Context,
	id string,
) error {
	if err := s.storage.RemoveClient(ctx, id); err != nil {
		if errors.Is(err, ErrClientNotFound) {
			slog.Warn("client not found during deletion", "client_id", id)
			return ErrClientNotFound
		}
		slog.Error("failed to delete client", "error", err, "client_id", id)
		return err
	}

	s.mu.Lock()
	delete(s.rateLimiter, id)
	s.mu.Unlock()

	slog.Info("client deleted successfully", "client_id", id)
	return nil
}

// IsRequestAllowed checks wheter request is allowed
func (s *LoadBalancerServiceImpl) IsRequestAllowed(ctx context.Context, clientID string) (bool, error) {
	s.mu.RLock()
	bucket, exists := s.rateLimiter[clientID]
	s.mu.RUnlock()

	if !exists {
		slog.Info("response from a new client")

		s.mu.Lock()
		defer s.mu.Unlock()

		newClient := models.Client{
			ClientID:   clientID,
			Capacity:   s.defaultCapacity,
			RatePerSec: s.defaultRatePerSec,
		}

		slog.Info("adding new client to database")
		if err := s.storage.SaveClient(ctx, newClient); err != nil {
			slog.Error("failed to save client to database",
				"error", err, "client_id", clientID)
		} else {
			slog.Info("created client with default values in database",
				"client_id", clientID)
		}

		s.rateLimiter[clientID] = &TokenBucket{
			Capacity:   s.defaultCapacity,
			Tokens:     s.defaultCapacity,
			RatePerSec: s.defaultRatePerSec,
			LastRefill: time.Now(),
		}
		bucket = s.rateLimiter[clientID]
	}

	return bucket.Tokens > 0, nil
}

// CountRequest removes a token from a bucket for a certain client
func (s *LoadBalancerServiceImpl) CountRequest(ctx context.Context, clientID string) (bool, error) {
	s.mu.RLock()
	bucket, exists := s.rateLimiter[clientID]
	s.mu.RUnlock()

	if !exists {
		return false, errors.New("client bucket not found")
	}

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	if bucket.Tokens > 0 {
		bucket.Tokens--
		slog.Info("token removed", "clientID", clientID, "tokens left", bucket.Tokens)
		return true, nil
	}

	return false, nil
}

// Shutdown shutdowns a server
func (s *LoadBalancerServiceImpl) Shutdown() {
	close(s.done)
}
