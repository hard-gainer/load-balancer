package storage

import (
	"context"
	"fmt"

	"github.com/hard-gainer/load-balancer/internal/config"
	"github.com/hard-gainer/load-balancer/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is a main storage interface
type Repository interface {
	GetAllClients(ctx context.Context) ([]models.Client, error)
	SaveClient(ctx context.Context, client models.Client) error
	UpdateClient(ctx context.Context, client models.Client) error
	RemoveClient(ctx context.Context, clientID string) error
	Close()
}

// qurier is set of needed methods from pgx
type qurier interface {
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	Close()
}

// PostgresRepository is Repository and qurier implementation
type PostgresRepository struct {
	storage qurier
}

// NewPostgres creates a new PostgreSQL repository with connection pool
func NewPostgres(cfg *config.Config) (Repository, error) {
	const op = "storage.NewPostgres"

	connPool, err := pgxpool.New(context.Background(), cfg.DBURL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	if err := connPool.Ping(context.Background()); err != nil {
		connPool.Close()
		return nil, fmt.Errorf("%s: could not ping database: %w", op, err)
	}

	return &PostgresRepository{storage: connPool}, nil
}

// GetAllClients retrieves all clients from the storage
func (repo *PostgresRepository) GetAllClients(ctx context.Context) ([]models.Client, error) {
	const op = "storage.GetAllClients"
	clients := make([]models.Client, 0)

	rows, err := repo.storage.Query(ctx,
		`SELECT	id, client_id, capacity, rate_per_sec, created_at
		 FROM clients`)
	if err != nil {
		return nil, fmt.Errorf("%s: query error: %w", op, err)
	}
	defer rows.Close()

	for rows.Next() {
		var client models.Client
		err := rows.Scan(
			&client.ID,
			&client.ClientID,
			&client.Capacity,
			&client.RatePerSec,
			&client.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("%s: scan error: %w", op, err)
		}
		clients = append(clients, client)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: rows iteration error: %w", op, err)
	}

	return clients, nil
}

// SaveClient saves the client into the storage
func (repo *PostgresRepository) SaveClient(ctx context.Context, client models.Client) error {
	const op = "storage.SaveClient"

	_, err := repo.storage.Exec(ctx,
		`INSERT INTO clients (client_id, capacity, rate_per_sec)
         VALUES ($1, $2, $3)`,
		client.ClientID, client.Capacity, client.RatePerSec)
	if err != nil {
		return fmt.Errorf("%s: exec error: %w", op, err)
	}

	return nil
}

// UpdateClient updates the client in the storage
func (repo *PostgresRepository) UpdateClient(ctx context.Context, client models.Client) error {
	const op = "storage.UpdateClient"

	result, err := repo.storage.Exec(ctx,
		`UPDATE clients 
         SET capacity = $1, rate_per_sec = $2
         WHERE client_id = $3`,
		client.Capacity, client.RatePerSec, client.ClientID)
	if err != nil {
		return fmt.Errorf("%s: exec error: %w", op, err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("%s: client not found", op)
	}

	return nil
}

// RemoveClient deletes the client from the storage
func (repo *PostgresRepository) RemoveClient(ctx context.Context, clientID string) error {
	const op = "storage.RemoveClient"

	result, err := repo.storage.Exec(ctx,
		`DELETE FROM clients WHERE client_id = $1`,
		clientID)
	if err != nil {
		return fmt.Errorf("%s: exec error: %w", op, err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("%s: client not found", op)
	}

	return nil
}

// Close closes a connection with a database
func (repo *PostgresRepository) Close() {
	repo.storage.Close()
}
