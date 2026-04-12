package billing

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// PriceTracker fetches BTC/USD price periodically.
type PriceTracker struct {
	mu    sync.RWMutex
	price float64 // current BTC/USD
}

// NewPriceTracker starts a background price feed. Fetches immediately then every 5 minutes.
func NewPriceTracker() *PriceTracker {
	pt := &PriceTracker{}
	pt.fetch() // initial fetch
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			pt.fetch()
		}
	}()
	return pt
}

// Price returns the latest BTC/USD price.
func (pt *PriceTracker) Price() float64 {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.price
}

func (pt *PriceTracker) fetch() {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd")
	if err != nil {
		log.Printf("routecat: btc price fetch: %v", err)
		return
	}
	defer resp.Body.Close()

	var data struct {
		Bitcoin struct {
			USD float64 `json:"usd"`
		} `json:"bitcoin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Printf("routecat: btc price parse: %v", err)
		return
	}
	if data.Bitcoin.USD > 0 {
		pt.mu.Lock()
		pt.price = data.Bitcoin.USD
		pt.mu.Unlock()
		log.Printf("routecat: BTC/USD = $%.0f", data.Bitcoin.USD)
	}
}
