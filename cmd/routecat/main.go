// RouteCat — open-source AI inference gateway.
// Routes user requests to provider nodes, meters tokens, pays via Lightning.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/aaronFortuno/routecat/internal/api"
	"github.com/aaronFortuno/routecat/internal/billing"
	"github.com/aaronFortuno/routecat/internal/gateway"
	"github.com/aaronFortuno/routecat/internal/lightning"
	"github.com/aaronFortuno/routecat/internal/router"
	"github.com/aaronFortuno/routecat/internal/store"
)

const version = "0.1.0"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	addr := flag.String("addr", ":8080", "gateway listen address")
	dbPath := flag.String("db", "routecat.db", "SQLite database path")
	lndAddr := flag.String("lnd-addr", "", "LND gRPC address (e.g. 192.168.1.100:10009)")
	lndMacaroon := flag.String("lnd-macaroon", "", "path to LND admin.macaroon")
	lndTLS := flag.String("lnd-tls", "", "path to LND tls.cert")
	feePct := flag.Float64("fee", 5.0, "gateway fee percentage (default 5%)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("routecat %s\n", version)
		os.Exit(0)
	}

	// Database
	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("routecat: database: %v", err)
	}
	defer db.Close()

	// Lightning (optional — gateway works without it, payouts queue)
	var ln lightning.Client
	if *lndAddr != "" {
		ln, err = lightning.NewLND(*lndAddr, *lndMacaroon, *lndTLS)
		if err != nil {
			log.Printf("routecat: WARNING — LND not available: %v (payouts disabled)", err)
		} else {
			log.Printf("routecat: LND connected at %s", *lndAddr)
		}
	} else {
		log.Printf("routecat: no LND configured — payouts disabled")
	}

	// Core services
	bill := billing.New(db, *feePct)
	rt := router.New()
	gw := gateway.New(db, rt, bill)
	pub := api.New(rt, bill, db)

	// Wire dependencies
	rt.SetSource(gw)        // router reads live node state from gateway
	pub.SetAssigner(gw)     // API sends jobs via gateway
	pub.SetNodeCounter(gw)  // API reads node count from gateway
	if ln != nil {
		pub.SetInvoicer(ln)    // API creates invoices via LND
		lightning.NewInvoiceWatcher(db, ln) // check paid invoices every 5s
		log.Printf("routecat: invoice watcher started")
	}

	// Payout engine (if LND connected)
	if ln != nil {
		lightning.NewPayoutEngine(db, ln, 10000) // max 10,000 sats/hour safety cap
		log.Printf("routecat: payout engine started (cap: 10,000 sats/hour)")
	}

	// HTTP server
	srv := gateway.NewServer(*addr, gw, pub, ln)
	if err := srv.Start(); err != nil {
		log.Fatalf("routecat: server: %v", err)
	}
	log.Printf("routecat: listening on %s (fee %.1f%%)", *addr, *feePct)

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("routecat: shutting down")
	srv.Stop()
}
