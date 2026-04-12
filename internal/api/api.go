// Package api serves the public OpenAI-compatible API for inference buyers.
// Users send chat completion requests which are routed to provider nodes.
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"crypto/rand"
	"encoding/hex"

	"github.com/aaronFortuno/routecat/internal/billing"
	"github.com/aaronFortuno/routecat/internal/router"
	"github.com/aaronFortuno/routecat/internal/store"
	"github.com/google/uuid"
)

// HandleRegisterUser creates a new user API key.
// POST /v1/auth/register with {"name": "my app"}
func (a *API) HandleRegisterUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = "default"
	}

	// Generate API key
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, `{"error":"key generation failed"}`, http.StatusInternalServerError)
		return
	}
	key := "rc_" + hex.EncodeToString(b)
	userID := uuid.New().String()

	if err := a.db.CreateUserKey(key, userID, req.Name, 100); err != nil {
		http.Error(w, `{"error":"failed to create key"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"api_key":     key,
		"user_id":     userID,
		"name":        req.Name,
		"quota_daily": 100,
	})
}

// JobAssigner sends a job to a node and returns a pending job handle.
type JobAssigner interface {
	AssignJob(nodeID, jobID, model, userKey string, buyerBody []byte, freeTier bool) (responseCh <-chan []byte, doneCh <-chan struct{}, err error)
}

// API handles public-facing inference requests.
type API struct {
	rt     *router.Router
	bill   *billing.Engine
	db     *store.DB
	assign JobAssigner
}

// New creates the public API.
func New(rt *router.Router, bill *billing.Engine, db *store.DB) *API {
	return &API{rt: rt, bill: bill, db: db}
}

// SetAssigner wires the gateway's job assignment function.
func (a *API) SetAssigner(j JobAssigner) { a.assign = j }

// HandleChatCompletions processes POST /v1/chat/completions (OpenAI compatible).
func (a *API) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"method not allowed"}}`, http.StatusMethodNotAllowed)
		return
	}

	// Authenticate user
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, `{"error":{"message":"missing API key","type":"invalid_request_error"}}`, http.StatusUnauthorized)
		return
	}
	userKey := strings.TrimPrefix(auth, "Bearer ")

	// Validate user key and quota
	_, remaining, err := a.db.ValidateUserKey(userKey)
	if err != nil {
		http.Error(w, `{"error":{"message":"invalid API key","type":"invalid_request_error"}}`, http.StatusUnauthorized)
		return
	}
	if remaining <= 0 {
		http.Error(w, `{"error":{"message":"daily quota exceeded","type":"rate_limit_error"}}`, http.StatusTooManyRequests)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
		return
	}

	// Extract model from request
	var req struct {
		Model  string `json:"model"`
		Stream *bool  `json:"stream,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		http.Error(w, `{"error":{"message":"model is required"}}`, http.StatusBadRequest)
		return
	}

	// Route to a node
	nodeID, err := a.rt.RouteRequest(req.Model)
	if err != nil {
		http.Error(w, `{"error":{"message":"no available node for model `+req.Model+`","type":"server_error"}}`, http.StatusServiceUnavailable)
		return
	}

	if a.assign == nil {
		http.Error(w, `{"error":{"message":"gateway not ready"}}`, http.StatusServiceUnavailable)
		return
	}

	// Assign job to node
	jobID := uuid.New().String()
	responseCh, doneCh, err := a.assign.AssignJob(nodeID, jobID, req.Model, userKey, body, false)
	if err != nil {
		http.Error(w, `{"error":{"message":"node unavailable","type":"server_error"}}`, http.StatusBadGateway)
		return
	}

	// Stream response back to buyer (SSE)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, canFlush := w.(http.Flusher)

	for {
		select {
		case chunk, ok := <-responseCh:
			if !ok {
				return // channel closed
			}
			w.Write(chunk) //nolint:errcheck
			if canFlush {
				flusher.Flush()
			}
		case <-doneCh:
			return
		case <-r.Context().Done():
			return // buyer disconnected
		}
	}
}

// HandleModels serves GET /v1/models — list of available models with pricing.
func (a *API) HandleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	type modelEntry struct {
		ID      string                 `json:"id"`
		Object  string                 `json:"object"`
		Pricing *billing.ModelPricing  `json:"routecat_pricing,omitempty"`
	}

	// Only show models that are actually available on connected nodes
	available := a.rt.AvailableModels()
	var models []modelEntry
	for _, tag := range available {
		entry := modelEntry{ID: tag, Object: "model"}
		if p := a.bill.GetPricing(tag); p != nil {
			entry.Pricing = p
		}
		models = append(models, entry)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   models,
	})
}
