package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hard-gainer/load-balancer/internal/api"
	"github.com/hard-gainer/load-balancer/internal/config"
	"github.com/hard-gainer/load-balancer/internal/models"
	"github.com/hard-gainer/load-balancer/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testClient = models.Client{
	ClientID:   "test-client",
	Capacity:   5,
	RatePerSec: 1,
}

// setupTestEnvironment setups testing environment with mock backends and test storage
func setupTestEnvironment(t testing.TB) (
	*httptest.Server,
	[]*httptest.Server,
	*InMemoryRepository,
	service.LoadBalancerService,
	func(),
) {
	backends := make([]*httptest.Server, 0, 3)
	backendModels := make([]models.Backend, 0, 3)

	// creating 2 test backends with different weights
	for i := 0; i < 2; i++ {
		backendID := i + 1
		weight := 1

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Response from backend %d", backendID)
		}))

		backends = append(backends, server)
		backendModels = append(backendModels, models.Backend{
			URL:    server.URL,
			Weight: weight,
		})
	}

	repo := NewInMemoryRepository().(*InMemoryRepository)

	// saving test client
	ctx := context.Background()
	err := repo.SaveClient(ctx, testClient)
	require.NoError(t, err, "Failed to save client")

	testConfig := &config.Config{
		ClientDefaultVals: config.ClientDefaultVals{
			Capacity:   10,
			RatePerSec: 1,
		},
	}

	service, err := service.NewLoadBalancerService(repo, testConfig)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	handler := api.NewLoadBalancerHandler(service, backendModels)

	mux := http.NewServeMux()
	mux.HandleFunc("/request", handler.HandleRequest)
	mux.HandleFunc("/clients", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.FindAllClients(w, r)
		case http.MethodPost:
			handler.CreateClient(w, r)
		case http.MethodPut:
			handler.UpdateClient(w, r)
		case http.MethodDelete:
			handler.DeleteClient(w, r)
		}
	})

	server := httptest.NewServer(mux)

	cleanup := func() {
		server.Close()
		for _, backend := range backends {
			backend.Close()
		}
		service.Shutdown()
	}

	return server, backends, repo, service, cleanup
}

// TestRoundRobinDistribution tests round-robin algorithm
func TestRoundRobinDistribution(t *testing.T) {
	server, _, _, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	responses := make(map[string]int)
	totalRequests := testClient.Capacity + 1

	for i := 0; i < totalRequests; i++ {
		req, _ := http.NewRequest("GET", server.URL+"/request", nil)
		req.Header.Set("client_id", testClient.ClientID)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, "Request failed")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Failed to read response body")
		resp.Body.Close()

		response := string(body)
		responses[response]++
	}

	expectedDistribution := map[string]int{
		"Response from backend 1": totalRequests / 2,
		"Response from backend 2": totalRequests / 2,
	}

	for response, count := range responses {
		expected := expectedDistribution[response]
		assert.InDelta(t, expected, count, 1,
			"Response %s should occur ~%d times, got %d", response, expected, count)
	}
}

// TestRateLimiting tests rate limiting algorithm
func TestRateLimiting(t *testing.T) {
	server, _, _, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	statusCodes := make([]int, 0, 10)

	attempts := testClient.Capacity + 5
	for i := 0; i < attempts; i++ {
		req, _ := http.NewRequest("GET", server.URL+"/request", nil)
		req.Header.Set("client_id", testClient.ClientID)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, "Request failed")

		statusCodes = append(statusCodes, resp.StatusCode)
		resp.Body.Close()

		time.Sleep(10 * time.Millisecond)
	}

	limitedCount := 0
	for _, code := range statusCodes {
		if code == 429 {
			limitedCount++
		}
	}

	assert.Greater(t, limitedCount, 0,
		"Expected some requests to be rate limited (429), but all succeeded")
	assert.Less(t, limitedCount, attempts,
		"Expected some requests to succeed, but all were rate limited")

	t.Logf("Rate limited %d out of %v requests", limitedCount, attempts)
}

// TestClientCRUD tests client management API
func TestClientCRUD(t *testing.T) {
	server, _, _, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	createPayload := `{"client_id":"crud-test","capacity":20,"rate_per_sec":5}`

	createResp, err := http.Post(
		server.URL+"/clients",
		"application/json",
		bytes.NewBufferString(createPayload),
	)
	require.NoError(t, err, "Failed to create client")
	require.Equal(t, http.StatusCreated, createResp.StatusCode, "Expected status 201 Created")
	createResp.Body.Close()

	// testing FindAllClients
	readResp, err := http.Get(server.URL + "/clients")
	require.NoError(t, err, "Failed to get clients")

	body, err := io.ReadAll(readResp.Body)
	require.NoError(t, err, "Failed to read response body")
	readResp.Body.Close()

	var clientsResp api.FindAllClientsResponse
	err = json.Unmarshal(body, &clientsResp)
	require.NoError(t, err, "Failed to parse response")

	var foundClient *models.Client
	for i, c := range clientsResp.Clients {
		if c.ClientID == "crud-test" {
			foundClient = &clientsResp.Clients[i]
			break
		}
	}

	require.NotNil(t, foundClient, "Created client not found in list")
	assert.Equal(t, 20, foundClient.Capacity, "Client has incorrect capacity")
	assert.Equal(t, 5, foundClient.RatePerSec, "Client has incorrect rate_per_sec")

	// testing UpdateClient
	updatePayload := `{"client_id":"crud-test","capacity":30,"rate_per_sec":10}`
	updateReq, _ := http.NewRequest(
		"PUT",
		server.URL+"/clients",
		bytes.NewBufferString(updatePayload),
	)
	updateReq.Header.Set("Content-Type", "application/json")

	updateResp, err := http.DefaultClient.Do(updateReq)
	require.NoError(t, err, "Failed to update client")
	require.Equal(t, http.StatusOK, updateResp.StatusCode, "Expected status 200 OK")
	updateResp.Body.Close()

	// checking updated data
	readResp2, err := http.Get(server.URL + "/clients")
	require.NoError(t, err, "Failed to get clients after update")

	body2, err := io.ReadAll(readResp2.Body)
	require.NoError(t, err, "Failed to read response body")
	readResp2.Body.Close()

	var clientsResp2 api.FindAllClientsResponse
	err = json.Unmarshal(body2, &clientsResp2)
	require.NoError(t, err, "Failed to parse response")

	var updatedClient *models.Client
	for i, c := range clientsResp2.Clients {
		if c.ClientID == "crud-test" {
			updatedClient = &clientsResp2.Clients[i]
			break
		}
	}

	require.NotNil(t, updatedClient, "Updated client not found in list")
	assert.Equal(t, 30, updatedClient.Capacity, "Client capacity not updated")
	assert.Equal(t, 10, updatedClient.RatePerSec, "Client rate_per_sec not updated")

	// testing DeleteClient
	deleteReq, _ := http.NewRequest("DELETE", server.URL+"/clients?client_id=crud-test", nil)
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	require.NoError(t, err, "Failed to delete client")
	require.Equal(t, http.StatusOK, deleteResp.StatusCode, "Expected status 200 OK")
	deleteResp.Body.Close()

	// checking client deletion
	readResp3, err := http.Get(server.URL + "/clients")
	require.NoError(t, err, "Failed to get clients after delete")

	body3, err := io.ReadAll(readResp3.Body)
	require.NoError(t, err, "Failed to read response body")
	readResp3.Body.Close()

	var clientsResp3 api.FindAllClientsResponse
	err = json.Unmarshal(body3, &clientsResp3)
	require.NoError(t, err, "Failed to parse response")

	for _, c := range clientsResp3.Clients {
		assert.NotEqual(t, "crud-test", c.ClientID, "Client should be deleted but still exists")
	}
}

// BenchmarkRequestThroughput measures the throughput of the load balancer
func BenchmarkRequestThroughput(b *testing.B) {
	server, _, _, _, cleanup := setupTestEnvironment(b)
	defer cleanup()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := http.Get(server.URL + "/request")
			if err != nil {
				b.Fatalf("Request failed: %v", err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}

// BenchmarkRateLimitedRequests performance benchmark under query rate limitation
func BenchmarkRateLimitedRequests(b *testing.B) {
	server, _, repo, _, cleanup := setupTestEnvironment(b)
	defer cleanup()

	client := models.Client{
		ClientID:   "bench-client",
		Capacity:   100,
		RatePerSec: 50,
	}

	ctx := context.Background()
	err := repo.SaveClient(ctx, client)
	if err != nil {
		b.Fatalf("Failed to save client: %v", err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("GET", server.URL+"/request", nil)
			req.Header.Set("client_id", client.ClientID)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				b.Fatalf("Request failed: %v", err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}

// BenchmarkConcurrentClients benchmark with multiple concurrent clients
func BenchmarkConcurrentClients(b *testing.B) {
	server, _, _, _, cleanup := setupTestEnvironment(b)
	defer cleanup()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		clientID := fmt.Sprintf("client-%d-%d", b.N, time.Now().UnixNano())

		for pb.Next() {
			req, _ := http.NewRequest("GET", server.URL+"/request", nil)
			req.Header.Set("client_id", clientID)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				b.Fatalf("Request failed: %v", err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}
