package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/hard-gainer/load-balancer/internal/models"
	"github.com/hard-gainer/load-balancer/internal/service"
	"github.com/hard-gainer/load-balancer/internal/storage"
)

type LoadBalancerHandler struct {
	loadBalancerService service.LoadBalancerService
	backends            []models.Backend
}

func NewLoadBalancerHandler(
	loadBalancerService service.LoadBalancerService,
	backends []models.Backend,
) *LoadBalancerHandler {
	return &LoadBalancerHandler{
		loadBalancerService: loadBalancerService,
	}
}

// ErrorResponse represents an API error response
type ErrorResponse struct {
	Error string `json:"error"`
}

type FindAllClientsResponse struct {
	Clients []models.Client `json:"clients,omitempty"`
}

// Client request/response types
type ClientRequest struct {
	ClientID   string `json:"client_id"`
	Capacity   int    `json:"capacity"`
	RatePerSec int    `json:"rate_per_sec"`
}

type ClientResponse struct {
	Client models.Client `json:"client"`
}

// func (h *LoadBalancerHandler) HandleRequest(w http.ResponseWriter, r *http.Request) {
// 	//
// 	h.backends
// 	proxy := httputil.NewSingleHostReverseProxy()
// }

// FindAllClients returns all clients
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

// CreateClient
func (h *LoadBalancerHandler) CreateClient(w http.ResponseWriter, r *http.Request) {
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

	err := h.loadBalancerService.CreateClient(r.Context(), client)
	if err != nil {
		slog.Error("failed to create client", "error", err, "client_id", req.ClientID)

		if errors.Is(err, storage.ErrClientAlreadyExists) {
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

// UpdateClient
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

		if errors.Is(err, storage.ErrClientNotFound) {
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

// DeleteClient
func (h *LoadBalancerHandler) DeleteClient(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		renderError(w, "ClientID is required", http.StatusBadRequest)
		return
	}

	err := h.loadBalancerService.DeleteClient(r.Context(), clientID)
	if err != nil {
		slog.Error("failed to delete client", "error", err, "client_id", clientID)

		if errors.Is(err, storage.ErrClientNotFound) {
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
