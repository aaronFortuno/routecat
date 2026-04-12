package gateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/aaronFortuno/routecat/internal/billing"
	"github.com/aaronFortuno/routecat/internal/router"
	"github.com/aaronFortuno/routecat/internal/store"
)

// Gateway manages connected provider nodes via WebSocket.
type Gateway struct {
	mu    sync.RWMutex
	nodes map[string]*NodeConn // node_id -> active connection
	db    *store.DB
	rt    *router.Router
	bill  *billing.Engine
}

// NodeConn represents a connected provider node.
type NodeConn struct {
	NodeID     string
	APIKey     string
	Models     []string
	VRAMFreeMB int
	Region     string
	QueueDepth int
	// ws connection will be added when we implement WebSocket
}

// New creates a Gateway.
func New(db *store.DB, rt *router.Router, bill *billing.Engine) *Gateway {
	return &Gateway{
		nodes: make(map[string]*NodeConn),
		db:    db,
		rt:    rt,
		bill:  bill,
	}
}

// HandleRegister processes POST /v1/gateway/register from provider nodes.
func (g *Gateway) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify API key from Authorization header
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	apiKey := strings.TrimPrefix(auth, "Bearer ")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var reg RegisterPayload
	if err := json.Unmarshal(body, &reg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if reg.APIKey != apiKey {
		http.Error(w, "key mismatch", http.StatusForbidden)
		return
	}

	modelsJSON, _ := json.Marshal(reg.Models)
	err = g.db.RegisterNode(store.Node{
		NodeID:          reg.NodeID,
		APIKey:          reg.APIKey,
		GPU:             reg.GPU,
		GPUVendor:       reg.GPUVendor,
		VRAMTotalMB:     reg.VRAMTotalMB,
		Models:          string(modelsJSON),
		Region:          reg.Region,
		LightningAddr:   reg.LightningAddress,
		RedeemThreshold: reg.RedeemThreshold,
		FreeTierPct:     reg.FreeTierPct,
		Version:         reg.Version,
	})
	if err != nil {
		log.Printf("routecat: register node %s: %v", reg.NodeID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Printf("routecat: node registered: %s (%s, %dMB VRAM, %d models)",
		reg.NodeID, reg.GPU, reg.VRAMTotalMB, len(reg.Models))
	w.WriteHeader(http.StatusCreated)
}

// HandleWS upgrades to WebSocket for the node control channel.
func (g *Gateway) HandleWS(w http.ResponseWriter, r *http.Request) {
	// TODO: upgrade to WS, authenticate via ?api_key=, enter read/write loop
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// HandleJobProxy serves GET /v1/gateway/jobs/{job_id}/proxy/request.
// The provider node fetches the buyer's request body from here.
func (g *Gateway) HandleJobProxy(w http.ResponseWriter, r *http.Request) {
	// TODO: look up job, return stored buyer request body
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// HandleWithdraw processes POST /v1/provider/withdraw-ecash.
func (g *Gateway) HandleWithdraw(w http.ResponseWriter, r *http.Request) {
	// TODO: generate ecash token from node balance
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// ConnectedNodes returns the count of active node connections.
func (g *Gateway) ConnectedNodes() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}
