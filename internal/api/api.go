// Package api serves the public OpenAI-compatible API for inference buyers.
// Users send chat completion requests which are routed to provider nodes.
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/aaronFortuno/routecat/internal/billing"
	"github.com/aaronFortuno/routecat/internal/router"
	"github.com/aaronFortuno/routecat/internal/store"
)

// API handles public-facing inference requests.
type API struct {
	rt   *router.Router
	bill *billing.Engine
	db   *store.DB
}

// New creates the public API.
func New(rt *router.Router, bill *billing.Engine, db *store.DB) *API {
	return &API{rt: rt, bill: bill, db: db}
}

// HandleChatCompletions processes POST /v1/chat/completions (OpenAI compatible).
func (a *API) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate user
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, `{"error":{"message":"missing API key","type":"invalid_request_error"}}`, http.StatusUnauthorized)
		return
	}

	// TODO: validate user API key, check quota
	// TODO: parse request body to extract model
	// TODO: route to a provider node via router
	// TODO: proxy the request through the gateway WS
	// TODO: stream response back to user (SSE)

	http.Error(w, `{"error":{"message":"not implemented","type":"server_error"}}`, http.StatusNotImplemented)
}

// HandleModels serves GET /v1/models — list of available models with pricing.
func (a *API) HandleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	type modelEntry struct {
		ID      string                  `json:"id"`
		Object  string                  `json:"object"`
		Pricing *billing.ModelPricing   `json:"routecat_pricing,omitempty"`
	}

	var models []modelEntry
	for tag, p := range a.bill.AllPricing() {
		pc := p // copy for pointer
		models = append(models, modelEntry{
			ID:      tag,
			Object:  "model",
			Pricing: &pc,
		})
	}

	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   models,
	})
}
