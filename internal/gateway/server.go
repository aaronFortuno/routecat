package gateway

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/aaronFortuno/routecat/internal/api"
	"github.com/aaronFortuno/routecat/internal/lightning"
)

// Server is the main HTTP server hosting the gateway WS, public API, and frontend.
type Server struct {
	addr string
	gw   *Gateway
	api  *api.API
	ln   lightning.Client
	srv  *http.Server
}

// NewServer creates the HTTP server with all routes.
func NewServer(addr string, gw *Gateway, pub *api.API, ln lightning.Client) *Server {
	mux := http.NewServeMux()

	// Gateway endpoints (for provider nodes)
	mux.HandleFunc("/v1/gateway/register", gw.HandleRegister)
	mux.HandleFunc("/v1/gateway/ws", gw.HandleWS)
	mux.HandleFunc("/v1/gateway/jobs/", gw.HandleJobProxy)

	// Provider payout
	mux.HandleFunc("/v1/provider/withdraw-ecash", gw.HandleWithdraw)

	// Public API (for users/buyers — OpenAI compatible)
	mux.HandleFunc("/v1/chat/completions", pub.HandleChatCompletions)
	mux.HandleFunc("/v1/models", pub.HandleModels)
	mux.HandleFunc("/v1/auth/register", pub.HandleRegisterUser)

	// Frontend
	mux.HandleFunc("/", serveWeb)

	// Security: rate limit 60 req/min per IP, 1MB max body
	rl := NewRateLimiter(60, time.Minute)
	handler := SecurityMiddleware(rl, 1<<20, mux)

	return &Server{
		addr: addr,
		gw:   gw,
		api:  pub,
		ln:   ln,
		srv:  &http.Server{Handler: handler},
	}
}

// Start binds the port and serves in the background.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	go s.srv.Serve(ln) //nolint:errcheck
	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() {
	if err := s.srv.Shutdown(context.Background()); err != nil {
		log.Printf("routecat: shutdown: %v", err)
	}
}

func serveWeb(w http.ResponseWriter, r *http.Request) {
	// TODO: serve frontend from web/static/ or embedded FS
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte("<h1>RouteCat</h1><p>Open-source AI inference gateway</p>")) //nolint:errcheck
}
