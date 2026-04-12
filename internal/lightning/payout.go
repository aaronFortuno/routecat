package lightning

import (
	"log"
	"sync"
	"time"

	"github.com/aaronFortuno/routecat/internal/store"
)

// PayoutEngine periodically checks node balances and sends Lightning payouts.
type PayoutEngine struct {
	db            *store.DB
	ln            Client
	maxPerHour    int64 // spending cap in sats per hour (safety limit)
	mu            sync.Mutex
	spentThisHour int64
	hourStart     time.Time
}

// NewPayoutEngine creates and starts the payout loop.
// maxPerHour: maximum sats to pay out per hour (0 = unlimited).
func NewPayoutEngine(db *store.DB, ln Client, maxPerHour int64) *PayoutEngine {
	pe := &PayoutEngine{
		db:         db,
		ln:         ln,
		maxPerHour: maxPerHour,
		hourStart:  time.Now(),
	}
	go pe.loop()
	return pe
}

func (pe *PayoutEngine) loop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if pe.ln == nil {
			continue
		}
		pe.processPayouts()
	}
}

func (pe *PayoutEngine) processPayouts() {
	pe.mu.Lock()
	// Reset hourly counter if hour has passed
	if time.Since(pe.hourStart) > time.Hour {
		pe.spentThisHour = 0
		pe.hourStart = time.Now()
	}
	spent := pe.spentThisHour
	pe.mu.Unlock()

	nodes, err := pe.db.PendingPayouts()
	if err != nil {
		log.Printf("routecat: payout query: %v", err)
		return
	}

	for _, n := range nodes {
		balance, err := pe.db.NodeBalance(n.NodeID)
		if err != nil || balance <= 0 {
			continue
		}

		payoutSats := balance / 1000 // msats to sats
		if payoutSats < int64(n.RedeemThreshold) {
			continue
		}

		// Check spending cap
		if pe.maxPerHour > 0 && spent+payoutSats > pe.maxPerHour {
			log.Printf("routecat: payout to %s skipped — would exceed hourly cap (%d/%d sats)",
				n.NodeID, spent+payoutSats, pe.maxPerHour)
			continue
		}

		if n.LightningAddr == "" {
			continue
		}

		log.Printf("routecat: paying %d sats to %s (%s)", payoutSats, n.NodeID, n.LightningAddr)
		payHash, err := pe.ln.PayAddress(n.LightningAddr, payoutSats)
		if err != nil {
			log.Printf("routecat: payout failed for %s: %v", n.NodeID, err)
			if dbErr := pe.db.RecordPayout(n.NodeID, payoutSats*1000, "", "failed"); dbErr != nil {
				log.Printf("routecat: CRITICAL — failed to record failed payout for %s: %v", n.NodeID, dbErr)
			}
			continue
		}

		if dbErr := pe.db.RecordPayout(n.NodeID, payoutSats*1000, payHash, "confirmed"); dbErr != nil {
			log.Printf("routecat: CRITICAL — payment sent but DB record failed! node=%s hash=%s amount=%d: %v",
				n.NodeID, payHash, payoutSats, dbErr)
		}
		pe.mu.Lock()
		pe.spentThisHour += payoutSats
		pe.mu.Unlock()
		spent += payoutSats

		log.Printf("routecat: paid %d sats to %s — hash: %s", payoutSats, n.LightningAddr, payHash)
	}
}
