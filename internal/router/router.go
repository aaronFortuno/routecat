// Package router selects the best provider node for an inference request.
// Routing criteria: model match, VRAM availability, region proximity,
// queue depth, and karma score.
package router

import (
	"errors"
	"sync"
)

var ErrNoNode = errors.New("no available node for this model")

// NodeInfo is a snapshot of a live node's state, provided by the gateway.
type NodeInfo struct {
	NodeID     string
	Models     []string
	VRAMFreeMB int
	Region     string
	QueueDepth int
}

// NodeSource provides live node state to the router.
type NodeSource interface {
	LiveNodeInfos() []NodeInfo
}

// Router selects provider nodes for incoming inference requests.
type Router struct {
	mu     sync.RWMutex
	source NodeSource
}

// New creates a Router.
func New() *Router {
	return &Router{}
}

// SetSource wires the live node provider (gateway hub).
func (r *Router) SetSource(s NodeSource) {
	r.mu.Lock()
	r.source = s
	r.mu.Unlock()
}

// RouteRequest finds the best node for a given model.
// Selection: filter by model → sort by queue depth (lowest first) → pick first.
func (r *Router) RouteRequest(model string) (nodeID string, err error) {
	r.mu.RLock()
	src := r.source
	r.mu.RUnlock()
	if src == nil {
		return "", ErrNoNode
	}

	nodes := src.LiveNodeInfos()
	var best *NodeInfo
	for i := range nodes {
		n := &nodes[i]
		if !hasModel(n.Models, model) {
			continue
		}
		if best == nil || n.QueueDepth < best.QueueDepth {
			best = n
		}
	}
	if best == nil {
		return "", ErrNoNode
	}
	return best.NodeID, nil
}

// AvailableModels returns the deduplicated set of models across connected nodes.
func (r *Router) AvailableModels() []string {
	r.mu.RLock()
	src := r.source
	r.mu.RUnlock()
	if src == nil {
		return nil
	}

	seen := make(map[string]bool)
	var out []string
	for _, n := range src.LiveNodeInfos() {
		for _, m := range n.Models {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

func hasModel(models []string, target string) bool {
	for _, m := range models {
		if m == target {
			return true
		}
	}
	return false
}
