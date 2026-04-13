package lightning

import (
	"log"
	"time"

	"github.com/routecat/routecat/internal/store"
)

// InvoiceWatcher checks pending invoices and credits user balances when paid.
type InvoiceWatcher struct {
	db *store.DB
	ln Client
}

// NewInvoiceWatcher starts a background loop checking for paid invoices.
func NewInvoiceWatcher(db *store.DB, ln Client) *InvoiceWatcher {
	w := &InvoiceWatcher{db: db, ln: ln}
	go w.loop()
	return w
}

func (w *InvoiceWatcher) loop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if w.ln == nil {
			continue
		}

		// Expire old invoices
		if expired, err := w.db.ExpireOldInvoices(); err == nil && expired > 0 {
			log.Printf("routecat: expired %d old invoices", expired)
		}

		// Check pending invoices
		invoices, err := w.db.PendingInvoices()
		if err != nil {
			continue
		}
		for _, inv := range invoices {
			paid, err := w.ln.CheckInvoice(inv.PaymentHash)
			if err != nil {
				log.Printf("routecat: check invoice %s: %v", inv.PaymentHash[:8], err)
				continue
			}
			if !paid {
				continue
			}

			credited, err := w.db.CreditInvoice(inv.PaymentHash)
			if err != nil {
				log.Printf("routecat: CRITICAL — credit invoice %s: %v", inv.PaymentHash[:8], err)
				continue
			}
			if credited {
				log.Printf("routecat: invoice paid — credited %d msats (%d sats) to key %s...%s",
					inv.AmountMsats, inv.AmountMsats/1000, inv.UserKey[:6], inv.UserKey[len(inv.UserKey)-4:])
			}
		}
	}
}
