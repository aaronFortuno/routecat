package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

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

	// pendingJobs stores buyer request bodies for proxy fetch.
	jobsMu      sync.RWMutex
	pendingJobs map[string]*PendingJob // job_id -> pending job
}

// PendingJob holds a buyer's request while the node processes it.
type PendingJob struct {
	JobID       string
	NodeID      string
	UserKey     string
	Model       string
	BuyerBody   []byte            // raw OpenAI-format request
	ResponseCh  chan []byte        // proxy_chunk data sent here
	DoneCh      chan struct{}      // closed on proxy_done
	FreeTier    bool
	StartedAt   time.Time
	LastChunk   string            // last SSE data line (may contain usage)
}

// NodeConn represents a connected provider node with live state.
type NodeConn struct {
	mu         sync.Mutex
	NodeID     string
	APIKey     string
	Models     []string
	VRAMTotalMB int
	VRAMFreeMB int
	GPUUtilPct int
	TempC      int
	PowerW     float64
	Region     string
	QueueDepth int
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
}

// Send sends a JSON message to the node.
func (nc *NodeConn) Send(msg WSMsg) error {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	ctx, cancel := context.WithTimeout(nc.ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, nc.conn, msg)
}

// New creates a Gateway.
func New(db *store.DB, rt *router.Router, bill *billing.Engine) *Gateway {
	g := &Gateway{
		nodes:       make(map[string]*NodeConn),
		db:          db,
		rt:          rt,
		bill:        bill,
		pendingJobs: make(map[string]*PendingJob),
	}
	go g.jobCleanupLoop()
	return g
}

// jobCleanupLoop removes pending jobs older than 2 minutes (dead streams).
func (g *Gateway) jobCleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-2 * time.Minute)
		g.jobsMu.Lock()
		for id, job := range g.pendingJobs {
			if job.StartedAt.Before(cutoff) {
				close(job.DoneCh)
				delete(g.pendingJobs, id)
				log.Printf("routecat: expired stale job %s (node %s)", id, job.NodeID)
			}
		}
		g.jobsMu.Unlock()
	}
}

// HandleRegister processes POST /v1/gateway/register from provider nodes.
func (g *Gateway) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

	if subtle.ConstantTimeCompare([]byte(reg.APIKey), []byte(apiKey)) != 1 {
		http.Error(w, "key mismatch", http.StatusForbidden)
		return
	}

	// Validate Lightning address format
	if reg.LightningAddress != "" && !isValidLightningAddress(reg.LightningAddress) {
		http.Error(w, "invalid lightning address", http.StatusBadRequest)
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
	apiKey := r.URL.Query().Get("api_key")
	if apiKey == "" {
		http.Error(w, "missing api_key", http.StatusUnauthorized)
		return
	}

	// Look up node by API key
	node, err := g.db.NodeByAPIKey(apiKey)
	if err != nil {
		http.Error(w, "unknown api_key", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // nodes may connect without origin
	})
	if err != nil {
		log.Printf("routecat: ws accept: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())

	var models []string
	_ = json.Unmarshal([]byte(node.Models), &models)

	nc := &NodeConn{
		NodeID:      node.NodeID,
		APIKey:      node.APIKey,
		Models:      models,
		VRAMTotalMB: node.VRAMTotalMB,
		Region:      node.Region,
		conn:        conn,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Register in live map
	g.mu.Lock()
	old, existed := g.nodes[node.NodeID]
	g.nodes[node.NodeID] = nc
	g.mu.Unlock()

	// Close previous connection if node reconnected
	if existed && old != nil {
		old.cancel()
		old.conn.Close(websocket.StatusGoingAway, "replaced by new connection")
	}

	log.Printf("routecat: node connected: %s (%d models) [%d total]",
		node.NodeID, len(models), g.ConnectedNodes())

	// Enter read loop (blocks until disconnect)
	g.readLoop(nc)

	// Cleanup on disconnect
	g.mu.Lock()
	if g.nodes[node.NodeID] == nc {
		delete(g.nodes, node.NodeID)
	}
	g.mu.Unlock()
	cancel()
	conn.Close(websocket.StatusNormalClosure, "goodbye")
	log.Printf("routecat: node disconnected: %s [%d remaining]",
		node.NodeID, g.ConnectedNodes())
}

// readLoop processes incoming WebSocket messages from a node.
func (g *Gateway) readLoop(nc *NodeConn) {
	// Start ping ticker
	go g.pingLoop(nc)

	for {
		var msg WSMsg
		err := wsjson.Read(nc.ctx, nc.conn, &msg)
		if err != nil {
			if websocket.CloseStatus(err) != -1 || nc.ctx.Err() != nil {
				return // clean close or context cancelled
			}
			log.Printf("routecat: ws read %s: %v", nc.NodeID, err)
			return
		}

		switch msg.Type {
		case "heartbeat":
			g.handleHeartbeat(nc, msg)
		case "pong":
			// node responded to our ping — nothing to do
		case "accept":
			g.handleAccept(nc, msg)
		case "reject":
			g.handleReject(nc, msg)
		case "proxy_chunk":
			g.handleProxyChunk(nc, msg)
		case "proxy_done":
			g.handleProxyDone(nc, msg)
		default:
			log.Printf("routecat: unknown msg type %q from %s", msg.Type, nc.NodeID)
		}
	}
}

// pingLoop sends ping messages every 30s to keep the connection alive.
func (g *Gateway) pingLoop(nc *NodeConn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-nc.ctx.Done():
			return
		case <-ticker.C:
			if err := nc.Send(WSMsg{Type: "ping"}); err != nil {
				return
			}
		}
	}
}

// handleHeartbeat processes a node heartbeat and responds with ACK.
func (g *Gateway) handleHeartbeat(nc *NodeConn, msg WSMsg) {
	// Update live state
	nc.mu.Lock()
	nc.GPUUtilPct = msg.GPUUtilPct
	nc.VRAMFreeMB = msg.VRAMFreeMB
	nc.TempC = msg.TempC
	nc.PowerW = msg.PowerW
	nc.QueueDepth = msg.QueueDepth
	nc.mu.Unlock()

	// Query stats from DB
	balance, _ := g.db.NodeBalance(nc.NodeID)
	earnedToday, _ := g.db.NodeEarningsToday(nc.NodeID)
	earnedTotal, _ := g.db.NodeEarningsTotal(nc.NodeID)
	jobsToday, tokensToday, _ := g.db.NodeJobsToday(nc.NodeID)

	ack := WSMsg{
		Type:             "heartbeat_ack",
		Status:           "registered",
		JobsToday:        jobsToday,
		TokensToday:      tokensToday,
		EarnedTodaySats:  earnedToday,
		EarnedTotalSats:  earnedTotal,
		BalanceSats:      balance,
		QueueDepthGlobal: g.globalQueueDepth(),
	}

	if err := nc.Send(ack); err != nil {
		log.Printf("routecat: heartbeat_ack %s: %v", nc.NodeID, err)
	}
}

// handleAccept processes a job acceptance from a node.
func (g *Gateway) handleAccept(nc *NodeConn, msg WSMsg) {
	g.jobsMu.RLock()
	job, ok := g.pendingJobs[msg.JobID]
	g.jobsMu.RUnlock()
	if !ok {
		log.Printf("routecat: accept for unknown job %s from %s", msg.JobID, nc.NodeID)
		return
	}
	log.Printf("routecat: job %s accepted by %s (model %s)", job.JobID, nc.NodeID, job.Model)
}

// handleReject processes a job rejection from a node.
func (g *Gateway) handleReject(nc *NodeConn, msg WSMsg) {
	g.jobsMu.Lock()
	job, ok := g.pendingJobs[msg.JobID]
	if ok {
		close(job.DoneCh)
		delete(g.pendingJobs, msg.JobID)
	}
	g.jobsMu.Unlock()
	if ok {
		log.Printf("routecat: job %s rejected by %s: %s", msg.JobID, nc.NodeID, msg.Reason)
	}
}

// handleProxyChunk forwards a streaming chunk from the node to the buyer.
func (g *Gateway) handleProxyChunk(nc *NodeConn, msg WSMsg) {
	g.jobsMu.RLock()
	job, ok := g.pendingJobs[msg.JobID]
	g.jobsMu.RUnlock()
	if !ok || job.NodeID != nc.NodeID {
		return // ignore chunks from wrong node
	}
	// Track chunk containing usage stats for billing
	if strings.Contains(msg.Data, "\"usage\"") {
		// Extract just the SSE data line containing usage
		for _, line := range strings.Split(msg.Data, "\n") {
			if strings.Contains(line, "\"usage\"") {
				job.LastChunk = line
				break
			}
		}
		log.Printf("routecat: [debug] job %s usage chunk captured: %s", msg.JobID, job.LastChunk)
	}
	select {
	case job.ResponseCh <- []byte(msg.Data):
	default:
	}
}

// handleProxyDone signals end of streaming for a job.
func (g *Gateway) handleProxyDone(nc *NodeConn, msg WSMsg) {
	g.jobsMu.Lock()
	job, ok := g.pendingJobs[msg.JobID]
	if ok && job.NodeID != nc.NodeID {
		g.jobsMu.Unlock()
		log.Printf("routecat: SECURITY — node %s tried to complete job %s owned by %s", nc.NodeID, msg.JobID, job.NodeID)
		return
	}
	if ok {
		delete(g.pendingJobs, msg.JobID)
	}
	g.jobsMu.Unlock()
	if !ok {
		return
	}
	close(job.DoneCh)

	// Extract token usage from last SSE chunk
	tokensIn, tokensOut := parseUsage(job.LastChunk)
	btcPrice := g.bill.BtcPrice()
	grossUSD, providerMsats, feeMsats := g.bill.Calculate(job.Model, tokensIn, tokensOut, btcPrice)

	now := time.Now()
	if err := g.db.RecordJob(store.Job{
		JobID:       job.JobID,
		NodeID:      job.NodeID,
		UserKey:     job.UserKey,
		Model:       job.Model,
		TokensIn:    tokensIn,
		TokensOut:   tokensOut,
		EarnedMsats: providerMsats,
		FeeMsats:    feeMsats,
		FreeTier:    job.FreeTier,
		Status:      "complete",
		StartedAt:   job.StartedAt,
		CompletedAt: &now,
	}); err != nil {
		log.Printf("routecat: CRITICAL — failed to record job %s: %v (node NOT paid)", job.JobID, err)
		return
	}

	// Send job_complete to node
	_ = nc.Send(WSMsg{
		Type:      "job_complete",
		Model:     job.Model,
		Tokens:    tokensIn + tokensOut,
		EarnedUSD: grossUSD * (1 - g.bill.FeePct()/100),
	})

	log.Printf("routecat: job %s complete — %d+%d tokens, $%.6f (provider: %d msats, fee: %d msats)",
		job.JobID, tokensIn, tokensOut, grossUSD, providerMsats, feeMsats)
}

// parseUsage extracts prompt_tokens and completion_tokens from SSE data.
// Handles multiple formats: raw JSON, "data: {json}", or multi-line SSE.
func parseUsage(sseData string) (tokensIn, tokensOut int) {
	// Try each line — usage might be in any of them
	for _, line := range strings.Split(sseData, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "data: ")
		line = strings.TrimPrefix(line, "data:")
		line = strings.TrimSpace(line)
		if line == "" || line == "[DONE]" || !strings.Contains(line, "usage") {
			continue
		}
		var chunk struct {
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage,omitempty"`
		}
		if err := json.Unmarshal([]byte(line), &chunk); err == nil && chunk.Usage != nil {
			return chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens
		}
	}
	return 0, 0
}

// HandleJobProxy serves GET /v1/gateway/jobs/{job_id}/proxy/request.
// The provider node fetches the buyer's original request body from here.
func (g *Gateway) HandleJobProxy(w http.ResponseWriter, r *http.Request) {
	// Extract job_id from path: /v1/gateway/jobs/{job_id}/proxy/request
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 6 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	jobID := parts[4]

	g.jobsMu.RLock()
	job, ok := g.pendingJobs[jobID]
	g.jobsMu.RUnlock()
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(job.BuyerBody) //nolint:errcheck
}

// HandleWithdraw processes POST /v1/provider/withdraw-ecash.
func (g *Gateway) HandleWithdraw(w http.ResponseWriter, r *http.Request) {
	// TODO: generate ecash token from node balance
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// AssignJob sends a job to a specific node and returns channels for streaming.
// Implements api.JobAssigner.
func (g *Gateway) AssignJob(nodeID, jobID, model, userKey string, buyerBody []byte, freeTier bool) (<-chan []byte, <-chan struct{}, error) {
	g.mu.RLock()
	nc, ok := g.nodes[nodeID]
	g.mu.RUnlock()
	if !ok {
		return nil, nil, router.ErrNoNode
	}

	job := &PendingJob{
		JobID:      jobID,
		NodeID:     nodeID,
		UserKey:    userKey,
		Model:      model,
		BuyerBody:  buyerBody,
		ResponseCh: make(chan []byte, 64),
		DoneCh:     make(chan struct{}),
		FreeTier:   freeTier,
		StartedAt:  time.Now(),
	}

	g.jobsMu.Lock()
	g.pendingJobs[jobID] = job
	g.jobsMu.Unlock()

	err := nc.Send(WSMsg{
		Type:  "job",
		JobID: jobID,
		Model: model,
	})
	if err != nil {
		g.jobsMu.Lock()
		delete(g.pendingJobs, jobID)
		g.jobsMu.Unlock()
		return nil, nil, err
	}

	return job.ResponseCh, job.DoneCh, nil
}

// GetNode returns a connected node by ID, or nil if not connected.
func (g *Gateway) GetNode(nodeID string) *NodeConn {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.nodes[nodeID]
}

// ConnectedNodes returns the count of active node connections.
func (g *Gateway) ConnectedNodes() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

// LiveNodes returns a snapshot of all connected nodes for the router.
func (g *Gateway) LiveNodes() []*NodeConn {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*NodeConn, 0, len(g.nodes))
	for _, nc := range g.nodes {
		out = append(out, nc)
	}
	return out
}

// LiveNodeInfos returns snapshots for the router (implements router.NodeSource).
func (g *Gateway) LiveNodeInfos() []router.NodeInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]router.NodeInfo, 0, len(g.nodes))
	for _, nc := range g.nodes {
		nc.mu.Lock()
		out = append(out, router.NodeInfo{
			NodeID:     nc.NodeID,
			Models:     nc.Models,
			VRAMFreeMB: nc.VRAMFreeMB,
			Region:     nc.Region,
			QueueDepth: nc.QueueDepth,
		})
		nc.mu.Unlock()
	}
	return out
}

// isValidLightningAddress checks basic format: user@domain, no spaces, reasonable length.
func isValidLightningAddress(addr string) bool {
	if len(addr) < 5 || len(addr) > 320 {
		return false
	}
	parts := strings.SplitN(addr, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	// Domain must have at least one dot
	if !strings.Contains(parts[1], ".") {
		return false
	}
	return true
}

// globalQueueDepth sums queue depth across all connected nodes.
func (g *Gateway) globalQueueDepth() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	total := 0
	for _, nc := range g.nodes {
		nc.mu.Lock()
		total += nc.QueueDepth
		nc.mu.Unlock()
	}
	return total
}
