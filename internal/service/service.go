package service

import (
	"context"

	"github.com/hard-gainer/load-balancer/internal/models"
	"github.com/hard-gainer/load-balancer/internal/storage"
)

type LoadBalancerService interface {
	FindAllClients(ctx context.Context, backends []models.Backend) ([]models.Client, error)
	CreateClient(ctx context.Context, client models.Client) error
	UpdateClient(ctx context.Context, client models.Client) error
	DeleteClient(ctx context.Context, id string) error
}

type LoadBalancerServiceImpl struct {
	storage storage.Repository
}

func NewLoadBalancerService(storage storage.Repository) LoadBalancerService {
	return &LoadBalancerServiceImpl{
		storage: storage,
	}
}

func (s *LoadBalancerServiceImpl) FindAllClients(
	ctx context.Context,
	backends []models.Backend,
) ([]models.Client, error) {
	panic("not implemented") // TODO: Implement
}

func (s *LoadBalancerServiceImpl) CreateClient(
	ctx context.Context,
	client models.Client,
) error {
	panic("not implemented") // TODO: Implement
	// create client in db
	// 
}

func (s *LoadBalancerServiceImpl) UpdateClient(
	ctx context.Context,
	client models.Client,
) error {
	panic("not implemented") // TODO: Implement
}

func (s *LoadBalancerServiceImpl) DeleteClient(
	ctx context.Context,
	id string,
) error {
	panic("not implemented") // TODO: Implement
}
