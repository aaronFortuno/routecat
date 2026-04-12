// Package api serves the public OpenAI-compatible API for inference buyers.
// Users send chat completion requests which are routed to provider nodes.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aaronFortuno/routecat/internal/billing"
	"github.com/aaronFortuno/routecat/internal/router"
	"github.com/aaronFortuno/routecat/internal/store"
	"github.com/google/uuid"
)

// HandleRegisterUser creates a new user API key.
// POST /v1/auth/register with {"name": "my app"}
// Rate limited: 3 registrations per hour per IP.
func (a *API) HandleRegisterUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limit: 3 keys/hour/IP
	ip := r.RemoteAddr
	if realIP := r.Header.Get("X-Real-Ip"); realIP != "" {
		ip = realIP
	}
	a.regMu.Lock()
	cutoff := time.Now().Add(-time.Hour)
	reqs := a.regLimit[ip]
	valid := reqs[:0]
	for _, t := range reqs {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= 3 {
		a.regLimit[ip] = valid
		a.regMu.Unlock()
		http.Error(w, `{"error":"too many registrations — try again later"}`, http.StatusTooManyRequests)
		return
	}
	a.regLimit[ip] = append(valid, time.Now())
	a.regMu.Unlock()

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

	if err := a.db.CreateUserKey(key, userID, req.Name, 10); err != nil {
		http.Error(w, `{"error":"failed to create key"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"api_key":     key,
		"user_id":     userID,
		"name":        req.Name,
		"quota_daily": 10,
	})
}

// JobAssigner sends a job to a node and returns a pending job handle.
type JobAssigner interface {
	AssignJob(nodeID, jobID, model, userKey string, buyerBody []byte, freeTier bool) (responseCh <-chan []byte, doneCh <-chan struct{}, err error)
}

// InvoiceCreator generates Lightning invoices.
type InvoiceCreator interface {
	CreateInvoice(amountSats int64, memo string) (bolt11 string, paymentHash string, err error)
}

// API handles public-facing inference requests.
type API struct {
	rt          *router.Router
	bill        *billing.Engine
	db          *store.DB
	assign      JobAssigner
	invoicer    InvoiceCreator
	nodeCounter NodeCounter
	regLimit    map[string][]time.Time // IP -> registration timestamps (rate limit)
	regMu       sync.Mutex
}

// New creates the public API.
func New(rt *router.Router, bill *billing.Engine, db *store.DB) *API {
	return &API{rt: rt, bill: bill, db: db, regLimit: make(map[string][]time.Time)}
}

// SetAssigner wires the gateway's job assignment function.
func (a *API) SetAssigner(j JobAssigner) { a.assign = j }

// SetInvoicer wires the Lightning invoice generator.
func (a *API) SetInvoicer(ic InvoiceCreator) { a.invoicer = ic }

// HandleTopUp generates a Lightning invoice for the user to pay.
// POST /v1/auth/topup with {"amount_sats": 1000}
func (a *API) HandleTopUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, `{"error":"missing API key"}`, http.StatusUnauthorized)
		return
	}
	userKey := strings.TrimPrefix(auth, "Bearer ")

	// Verify key exists
	if _, _, err := a.db.ValidateUserKey(userKey); err != nil {
		http.Error(w, `{"error":"invalid API key"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		AmountSats int64 `json:"amount_sats"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AmountSats <= 0 {
		http.Error(w, `{"error":"amount_sats must be positive"}`, http.StatusBadRequest)
		return
	}
	if req.AmountSats < 10 || req.AmountSats > 100000 {
		http.Error(w, `{"error":"amount must be between 10 and 100,000 sats"}`, http.StatusBadRequest)
		return
	}

	if a.invoicer == nil {
		http.Error(w, `{"error":"payments not available"}`, http.StatusServiceUnavailable)
		return
	}

	bolt11, payHash, err := a.invoicer.CreateInvoice(req.AmountSats, "RouteCat top-up")
	if err != nil {
		http.Error(w, `{"error":"failed to create invoice"}`, http.StatusInternalServerError)
		return
	}

	amountMsats := req.AmountSats * 1000
	if err := a.db.CreateInvoice(payHash, userKey, amountMsats, bolt11); err != nil {
		http.Error(w, `{"error":"failed to store invoice"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"invoice":      bolt11,
		"payment_hash": payHash,
		"amount_sats":  req.AmountSats,
		"expires_in":   600,
	})
}

// HandleBalance returns the user's current balance.
// GET /v1/auth/balance
func (a *API) HandleBalance(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, `{"error":"missing API key"}`, http.StatusUnauthorized)
		return
	}
	userKey := strings.TrimPrefix(auth, "Bearer ")

	balance, err := a.db.UserBalance(userKey)
	if err != nil {
		http.Error(w, `{"error":"invalid API key"}`, http.StatusUnauthorized)
		return
	}

	_, remaining, _ := a.db.ValidateUserKey(userKey)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"balance_msats":    balance,
		"balance_sats":     balance / 1000,
		"free_remaining":   remaining,
	})
}

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

	// Validate user key
	_, remaining, err := a.db.ValidateUserKey(userKey)
	if err != nil {
		http.Error(w, `{"error":{"message":"invalid API key","type":"invalid_request_error"}}`, http.StatusUnauthorized)
		return
	}
	// Free tier only from playground (X-Playground header). API requires balance.
	balance, _ := a.db.UserBalance(userKey)
	isPlayground := r.Header.Get("X-Playground") == "true"
	if isPlayground && remaining > 0 {
		// Free playground request — allowed
	} else if balance > 0 {
		// Paid request — allowed
	} else if remaining > 0 && !isPlayground {
		http.Error(w, `{"error":{"message":"free tier is playground-only — top up at /v1/auth/topup for API access","type":"rate_limit_error"}}`, http.StatusTooManyRequests)
		return
	} else {
		http.Error(w, `{"error":{"message":"no balance — top up at /v1/auth/topup","type":"rate_limit_error"}}`, http.StatusTooManyRequests)
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

// NodeCounter provides live node count.
type NodeCounter interface {
	ConnectedNodes() int
}

// SetNodeCounter wires the gateway for node counting.
func (a *API) SetNodeCounter(nc NodeCounter) { a.nodeCounter = nc }

// HandleStats serves GET /v1/stats — live gateway statistics.
func (a *API) HandleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	nodes := 0
	if a.nodeCounter != nil {
		nodes = a.nodeCounter.ConnectedNodes()
	}
	json.NewEncoder(w).Encode(map[string]any{
		"nodes_online": nodes,
		"btc_usd":      a.bill.BtcPrice(),
		"fee_pct":      a.bill.FeePct(),
	})
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
	fallback := billing.FallbackPricing()
	var models []modelEntry
	for _, tag := range available {
		entry := modelEntry{ID: tag, Object: "model"}
		if p := a.bill.GetPricing(tag); p != nil {
			entry.Pricing = p
		} else {
			entry.Pricing = &fallback
		}
		models = append(models, entry)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   models,
	})
}
