// Package router selects the best provider node for an inference request.
// Routing criteria: model match, VRAM availability, region proximity,
// queue depth, and karma score.
package router

import (
	"errors"

	"github.com/aaronFortuno/routecat/internal/store"
)

var ErrNoNode = errors.New("no available node for this model")

// Router selects provider nodes for incoming inference requests.
type Router struct {
	db *store.DB
}

// New creates a Router.
func New(db *store.DB) *Router {
	return &Router{db: db}
}

// RouteRequest finds the best node for a given model and region.
// Returns the node ID of the selected provider.
// TODO: implement actual routing logic using connected node state,
// not just DB records. This will use the gateway's live NodeConn map.
func (r *Router) RouteRequest(model string, buyerRegion string) (nodeID string, err error) {
	// Placeholder — will be wired to the gateway's live connection pool.
	return "", ErrNoNode
}

// AvailableModels returns the set of models currently served by connected nodes.
func (r *Router) AvailableModels() []string {
	// TODO: aggregate from live connections
	return nil
}

// QueueDepthGlobal returns total pending jobs across all nodes.
func (r *Router) QueueDepthGlobal() int {
	// TODO: sum from live connections
	return 0
}
