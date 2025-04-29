package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"github.com/hard-gainer/load-balancer/internal/models"
	"github.com/hard-gainer/load-balancer/internal/service"
)

// LoadBalancerHandler is a main handler
type LoadBalancerHandler struct {
	loadBalancerService service.LoadBalancerService
	backends            []models.Backend
	mu                  sync.Mutex
	counter             int
	buffer              []models.Backend
}

// NewLoadBalancerHandler constructs a new LoadBalancerHandler
func NewLoadBalancerHandler(
	loadBalancerService service.LoadBalancerService,
	backends []models.Backend,
) *LoadBalancerHandler {
	return &LoadBalancerHandler{
		loadBalancerService: loadBalancerService,
		backends:            backends,
		counter:             0,
		buffer:              make([]models.Backend, 0),
	}
}

// ErrorResponse represents an API error response
type ErrorResponse struct {
	Error string `json:"error"`
}

// FindAllClientsResponse is a helper response structure
type FindAllClientsResponse struct {
	Clients []models.Client `json:"clients,omitempty"`
}

// ClientRequest is a helper request structure
type ClientRequest struct {
	ClientID   string `json:"client_id,omitempty"`
	Capacity   int    `json:"capacity,omitempty"`
	RatePerSec int    `json:"rate_per_sec,omitempty"`
}

// ClientResponse is a helper response structure
type ClientResponse struct {
	Client models.Client `json:"client"`
}

// HandleRequest processes incoming HTTP requests by applying rate limiting
// and performing reverse proxying to one of the available backend servers
// using the weighted round-robin algorithm.
func (h *LoadBalancerHandler) HandleRequest(w http.ResponseWriter, r *http.Request) {
	if len(h.backends) == 0 {
		slog.Error("no backends available")
		renderError(w, "No backends available", http.StatusServiceUnavailable)
		return
	}

	clientID := r.Header.Get("client_id")
	// if clientID is empty we use client's ip as a clientID
	if clientID == "" {
		clientID = r.RemoteAddr
	}

	slog.Info("request gained", "client_id", clientID)

	allowed, err := h.loadBalancerService.IsRequestAllowed(r.Context(), clientID)
	if err != nil {
		slog.Error("rate limiting error", "error", err, "client_id", clientID)
		renderError(w, "Rate limiting service error", http.StatusInternalServerError)
		return
	}

	if !allowed {
		slog.Info("rate limit exceeded", "client_id", clientID)
		w.Header().Set("Retry-After", "5")
		renderError(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	slog.Info("request allowed")

	h.mu.Lock()

	var target models.Backend
	backendFound := false

	// if h.buffer is empty - find new backend
	// otherwise pop backend from h.buffer
	if len(h.buffer) == 0 {
		startIdx := h.counter
		for i := 0; i < len(h.backends); i++ {
			currIdx := (startIdx + i) % len(h.backends)
			backend := h.backends[currIdx]

			_, err := url.Parse(backend.URL)
			if err != nil {
				slog.Error("failed to parse url", "url", backend.URL, "error", err)
				continue
			}

			h.counter = (currIdx + 1) % len(h.backends)
			// push backend to buffer weight-1 times
			for j := 0; j < backend.Weight-1; j++ {
				h.buffer = append(h.buffer, backend)
			}
			target = backend
			backendFound = true
			break
		}
	} else {
		target = h.buffer[0]
		h.buffer = h.buffer[1:]
		backendFound = true
	}

	h.mu.Unlock()

	if !backendFound {
		slog.Error("all backends are unavailable")
		renderError(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	success, err := h.loadBalancerService.CountRequest(r.Context(), clientID)
	if err != nil || !success {
		slog.Error("failed to count request", "error", err, "client_id", clientID)
		renderError(w, "Rate limiting error", http.StatusInternalServerError)
		return
	}

	slog.Info("reverse proxy the request", "client_id", clientID)

	targetURL, _ := url.Parse(target.URL)
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Error("backend error",
			"url", target.URL,
			"error", err,
			"method", r.Method,
			"path", r.URL.Path)
		renderError(w, "Backend server error", http.StatusBadGateway)
	}

	r.Header.Set("X-Forwarded-For", r.RemoteAddr)
	r.Header.Set("X-Forwarded-Host", r.Host)
	r.Header.Set("X-Forwarded-Proto", "http")

	slog.Info("forwarding request",
		"client", clientID,
		"method", r.Method,
		"path", r.URL.Path,
		"backend", target.URL)

	proxy.ServeHTTP(w, r)
}

// FindAllClients gets a list of all registered clients with their restrictions
// and returns them in JSON format
func (h *LoadBalancerHandler) FindAllClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.loadBalancerService.FindAllClients(r.Context(), h.backends)
	if err != nil {
		slog.Error("failed to find clients", "error", err)
		renderError(w, "Failed to find clients", http.StatusInternalServerError)
		return
	}

	resp := FindAllClientsResponse{
		Clients: clients,
	}
	renderJSON(w, resp, http.StatusOK)
}

// CreateClient processes a request to create a new client with the specified constraint
//  parameters.
// If ClientID is not specified, uses client IP address as identifier.
// Returns the 201 Created status when the client is successfully created.
func (h *LoadBalancerHandler) CreateClient(w http.ResponseWriter, r *http.Request) {
	var req ClientRequest
	if err := parseJSONBody(r, &req); err != nil {
		slog.Error("failed to parse request body", "error", err)
		renderError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	clientID := req.ClientID

	if req.ClientID == "" {
		clientID = r.RemoteAddr
	}

	if req.Capacity <= 0 {
		renderError(w, "Capacity must be a positive number", http.StatusBadRequest)
		return
	}

	if req.RatePerSec <= 0 {
		renderError(w, "RatePerSec must be a positive number", http.StatusBadRequest)
		return
	}

	client := models.Client{
		ClientID:   clientID,
		Capacity:   req.Capacity,
		RatePerSec: req.RatePerSec,
	}

	err := h.loadBalancerService.CreateClient(r.Context(), client)
	if err != nil {
		slog.Error("failed to create client", "error", err, "client_id", req.ClientID)

		if errors.Is(err, service.ErrClientAlreadyExists) {
			renderError(w, "Client with this ID already exists", http.StatusConflict)
			return
		}

		renderError(w, "Failed to create client", http.StatusInternalServerError)
		return
	}

	renderJSON(w, map[string]string{
		"status":  "success",
		"message": "Client created successfully",
	}, http.StatusCreated)
}

// UpdateClient updates the parameters of an existing client (Capacity and RatePerSec).
// Accepts ClientID, Capacity and RatePerSec via JSON request body.
// Returns an error if a client with the specified ID is not found.
// Returns 200 OK status on successful update.
func (h *LoadBalancerHandler) UpdateClient(w http.ResponseWriter, r *http.Request) {
	var req ClientRequest
	if err := parseJSONBody(r, &req); err != nil {
		slog.Error("failed to parse request body", "error", err)
		renderError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ClientID == "" {
		renderError(w, "ClientID is required", http.StatusBadRequest)
		return
	}

	if req.Capacity <= 0 {
		renderError(w, "Capacity must be a positive number", http.StatusBadRequest)
		return
	}

	if req.RatePerSec <= 0 {
		renderError(w, "RatePerSec must be a positive number", http.StatusBadRequest)
		return
	}

	client := models.Client{
		ClientID:   req.ClientID,
		Capacity:   req.Capacity,
		RatePerSec: req.RatePerSec,
	}

	err := h.loadBalancerService.UpdateClient(r.Context(), client)
	if err != nil {
		slog.Error("failed to update client", "error", err, "client_id", req.ClientID)

		if errors.Is(err, service.ErrClientNotFound) {
			renderError(w, "Client not found", http.StatusNotFound)
			return
		}

		renderError(w, "Failed to update client", http.StatusInternalServerError)
		return
	}

	renderJSON(w, map[string]string{
		"status":  "success",
		"message": "Client updated successfully",
	}, http.StatusOK)
}

// DeleteClient deletes a client by the specified clientID.
// Accepts client_id parameter via query parameters.
// Returns an error if the client with the specified ID is not found.
// If deletion is successful, returns status 200 OK.ient
func (h *LoadBalancerHandler) DeleteClient(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		renderError(w, "ClientID is required", http.StatusBadRequest)
		return
	}

	err := h.loadBalancerService.DeleteClient(r.Context(), clientID)
	if err != nil {
		slog.Error("failed to delete client", "error", err, "client_id", clientID)

		if errors.Is(err, service.ErrClientNotFound) {
			renderError(w, "Client not found", http.StatusNotFound)
			return
		}

		renderError(w, "Failed to delete client", http.StatusInternalServerError)
		return
	}

	renderJSON(w, map[string]string{
		"status":  "success",
		"message": "Client deleted successfully",
	}, http.StatusOK)
}

// Helper function to parse JSON body
func parseJSONBody(r *http.Request, dst interface{}) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	return json.Unmarshal(body, dst)
}

// renderJSON is a helper function for response formatting
func renderJSON(w http.ResponseWriter, data interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

// renderError is a helper function for error rendering
func renderError(w http.ResponseWriter, message string, status int) {
	resp := ErrorResponse{Error: message}
	renderJSON(w, resp, status)
}
