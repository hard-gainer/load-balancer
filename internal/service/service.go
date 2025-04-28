package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/hard-gainer/load-balancer/internal/models"
	"github.com/hard-gainer/load-balancer/internal/storage"
)

type TokenBucket struct {
	Capacity   int
	Tokens     int
	RatePerSec int
	LastRefill time.Time
	mu         sync.Mutex
}

type LoadBalancerService interface {
	FindAllClients(ctx context.Context, backends []models.Backend) ([]models.Client, error)
	CreateClient(ctx context.Context, client models.Client) error
	UpdateClient(ctx context.Context, client models.Client) error
	DeleteClient(ctx context.Context, id string) error
	IsRequestAllowed(ctx context.Context, clientID string) (bool, error)
	CountRequest(ctx context.Context, clientID string) (bool, error)
    Shutdown()
}

type LoadBalancerServiceImpl struct {
	storage     storage.Repository
	rateLimiter map[string]*TokenBucket
	mu          sync.RWMutex
	ticker      *time.Ticker
	done        chan struct{}
}

func NewLoadBalancerService(storage storage.Repository) (LoadBalancerService, error) {
	service := &LoadBalancerServiceImpl{
		storage:     storage,
		rateLimiter: make(map[string]*TokenBucket),
		ticker:      time.NewTicker(time.Second),
		done:        make(chan struct{}),
	}

	if err := service.InitRateLimiters(context.Background()); err != nil {
		return nil, err
	}

	go service.refillTokens()

	return service, nil
}

// InitRateLimiters loads all clients from a database to add them into the rateLimiter
func (s *LoadBalancerServiceImpl) InitRateLimiters(ctx context.Context) error {
	clients, err := s.storage.GetAllClients(ctx)
	if err != nil {
		slog.Error("failed to get clients" , "error", err)
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

func (s *LoadBalancerServiceImpl) FindAllClients(
	ctx context.Context,
	backends []models.Backend,
) ([]models.Client, error) {
	// const op = "service.FindAllClients"
	slog.Info("finding all clients", "backends_count", len(backends))

	clients, err := s.storage.GetAllClients(ctx)
	if err != nil {
		slog.Error("failed to get clients from storage", "error", err)
		return nil, err
	}

	return clients, nil
}

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

func (s *LoadBalancerServiceImpl) IsRequestAllowed(ctx context.Context, clientID string) (bool, error) {
	s.mu.RLock()
	bucket, exists := s.rateLimiter[clientID]
	s.mu.RUnlock()

	if !exists {
		// Если клиент не найден в rate limiter, пытаемся найти его в БД
		clients, err := s.storage.GetAllClients(ctx)
		if err != nil {
			slog.Error("failed to get clients for rate limiting", "error", err)
			return false, err
		}

		var clientFound bool
		var clientConfig models.Client

		for _, client := range clients {
			if client.ClientID == clientID {
				clientFound = true
				clientConfig = client
				break
			}
		}

		// Если клиент найден в БД, создаем для него бакет
		if clientFound {
			s.mu.Lock()
			s.rateLimiter[clientID] = &TokenBucket{
				Capacity:   clientConfig.Capacity,
				Tokens:     clientConfig.Capacity,
				RatePerSec: clientConfig.RatePerSec,
				LastRefill: time.Now(),
			}
			bucket = s.rateLimiter[clientID]
			s.mu.Unlock()
		} else {
			// Если клиент не найден в БД, используем дефолтные значения
			s.mu.Lock()
			s.rateLimiter[clientID] = &TokenBucket{
				Capacity:   10, // Дефолтная емкость
				Tokens:     10, // Начинаем с полного бакета
				RatePerSec: 1,  // Дефолтная скорость пополнения
				LastRefill: time.Now(),
			}
			bucket = s.rateLimiter[clientID]
			s.mu.Unlock()
		}
	}

	// Проверяем, есть ли токен для запроса
	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Перед проверкой пополняем токены
	s.refillBucket(bucket)

	return bucket.Tokens > 0, nil
}

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
		return true, nil
	}

	return false, nil
}

func (s *LoadBalancerServiceImpl) Shutdown() {
    close(s.done)
}
