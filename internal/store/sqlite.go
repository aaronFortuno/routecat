// Package store provides SQLite persistence for nodes, jobs, and payouts.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database with typed queries.
type DB struct {
	db *sql.DB
}

// Open creates or opens the SQLite database and runs migrations.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite single-writer
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS nodes (
			node_id       TEXT PRIMARY KEY,
			api_key       TEXT UNIQUE NOT NULL,
			gpu           TEXT,
			gpu_vendor    TEXT,
			vram_total_mb INTEGER,
			models        TEXT,  -- JSON array of model tags
			region        TEXT,
			lightning_addr TEXT,
			redeem_threshold INTEGER DEFAULT 500,
			free_tier_pct INTEGER DEFAULT 0,
			version       TEXT,
			last_seen     DATETIME,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS api_keys (
			key           TEXT PRIMARY KEY,
			user_id       TEXT NOT NULL,
			name          TEXT,
			quota_daily   INTEGER DEFAULT 10,
			balance_msats INTEGER DEFAULT 0,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS invoices (
			payment_hash  TEXT PRIMARY KEY,
			user_key      TEXT NOT NULL,
			amount_msats  INTEGER NOT NULL,
			bolt11        TEXT NOT NULL,
			status        TEXT DEFAULT 'pending',  -- pending, credited, expired
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_invoices_status ON invoices(status);

		CREATE TABLE IF NOT EXISTS jobs (
			job_id        TEXT PRIMARY KEY,
			node_id       TEXT NOT NULL,
			user_key      TEXT NOT NULL,
			model         TEXT NOT NULL,
			tokens_in     INTEGER DEFAULT 0,
			tokens_out    INTEGER DEFAULT 0,
			earned_msats  INTEGER DEFAULT 0,
			fee_msats     INTEGER DEFAULT 0,
			free_tier     BOOLEAN DEFAULT FALSE,
			status        TEXT DEFAULT 'pending',  -- pending, streaming, complete, failed
			started_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at  DATETIME
		);

		CREATE TABLE IF NOT EXISTS payouts (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id       TEXT NOT NULL,
			amount_msats  INTEGER NOT NULL,
			payment_hash  TEXT,
			status        TEXT DEFAULT 'pending',  -- pending, sent, confirmed, failed
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			confirmed_at  DATETIME
		);

		CREATE INDEX IF NOT EXISTS idx_jobs_node ON jobs(node_id);
		CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
		CREATE INDEX IF NOT EXISTS idx_payouts_node ON payouts(node_id);
	`)
	return err
}

// Node represents a registered provider node.
type Node struct {
	NodeID          string
	APIKey          string
	GPU             string
	GPUVendor       string
	VRAMTotalMB     int
	Models          string // JSON array
	Region          string
	LightningAddr   string
	RedeemThreshold int
	FreeTierPct     int
	Version         string
	LastSeen        time.Time
}

// Job represents a completed or in-progress inference job.
type Job struct {
	JobID       string
	NodeID      string
	UserKey     string
	Model       string
	TokensIn    int
	TokensOut   int
	EarnedMsats int64
	FeeMsats    int64
	FreeTier    bool
	Status      string
	StartedAt   time.Time
	CompletedAt *time.Time
}

// Payout represents a Lightning payment to a provider.
type Payout struct {
	ID           int64
	NodeID       string
	AmountMsats  int64
	PaymentHash  string
	Status       string
	CreatedAt    time.Time
	ConfirmedAt  *time.Time
}

// NodeByAPIKey looks up a node by its API key.
func (d *DB) NodeByAPIKey(apiKey string) (*Node, error) {
	var n Node
	err := d.db.QueryRow(`SELECT node_id, api_key, gpu, gpu_vendor, vram_total_mb, models, region, lightning_addr, redeem_threshold, free_tier_pct, version FROM nodes WHERE api_key=?`, apiKey).
		Scan(&n.NodeID, &n.APIKey, &n.GPU, &n.GPUVendor, &n.VRAMTotalMB, &n.Models, &n.Region, &n.LightningAddr, &n.RedeemThreshold, &n.FreeTierPct, &n.Version)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// NodeEarningsTotal returns lifetime msats earned by a node.
func (d *DB) NodeEarningsTotal(nodeID string) (int64, error) {
	var v sql.NullInt64
	err := d.db.QueryRow(`SELECT COALESCE(SUM(earned_msats),0) FROM jobs WHERE node_id=? AND status='complete'`, nodeID).Scan(&v)
	return v.Int64, err
}

// RegisterNode inserts or updates a node.
func (d *DB) RegisterNode(n Node) error {
	_, err := d.db.Exec(`
		INSERT INTO nodes (node_id, api_key, gpu, gpu_vendor, vram_total_mb, models, region, lightning_addr, redeem_threshold, free_tier_pct, version, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			gpu=excluded.gpu, gpu_vendor=excluded.gpu_vendor, vram_total_mb=excluded.vram_total_mb,
			models=excluded.models, region=excluded.region, lightning_addr=excluded.lightning_addr,
			redeem_threshold=excluded.redeem_threshold, free_tier_pct=excluded.free_tier_pct,
			version=excluded.version, last_seen=excluded.last_seen`,
		n.NodeID, n.APIKey, n.GPU, n.GPUVendor, n.VRAMTotalMB, n.Models,
		n.Region, n.LightningAddr, n.RedeemThreshold, n.FreeTierPct, n.Version, time.Now().UTC(),
	)
	return err
}

// RecordJob inserts a completed job.
func (d *DB) RecordJob(j Job) error {
	_, err := d.db.Exec(`
		INSERT INTO jobs (job_id, node_id, user_key, model, tokens_in, tokens_out, earned_msats, fee_msats, free_tier, status, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.JobID, j.NodeID, j.UserKey, j.Model, j.TokensIn, j.TokensOut,
		j.EarnedMsats, j.FeeMsats, j.FreeTier, j.Status, j.StartedAt, j.CompletedAt,
	)
	return err
}

// NodeBalance returns the unpaid balance in msats for a node.
func (d *DB) NodeBalance(nodeID string) (int64, error) {
	var earned, paid sql.NullInt64
	err := d.db.QueryRow(`SELECT COALESCE(SUM(earned_msats),0) FROM jobs WHERE node_id=? AND status='complete'`, nodeID).Scan(&earned)
	if err != nil {
		return 0, err
	}
	err = d.db.QueryRow(`SELECT COALESCE(SUM(amount_msats),0) FROM payouts WHERE node_id=? AND status IN ('sent','confirmed')`, nodeID).Scan(&paid)
	if err != nil {
		return 0, err
	}
	return earned.Int64 - paid.Int64, nil
}

// NodeEarningsToday returns msats earned today (UTC) by a node.
func (d *DB) NodeEarningsToday(nodeID string) (int64, error) {
	var v sql.NullInt64
	err := d.db.QueryRow(`SELECT COALESCE(SUM(earned_msats),0) FROM jobs WHERE node_id=? AND status='complete' AND date(started_at)=date('now')`, nodeID).Scan(&v)
	return v.Int64, err
}

// NodeJobsToday returns job count and total tokens for a node today.
func (d *DB) NodeJobsToday(nodeID string) (jobs int, tokens int, err error) {
	err = d.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(tokens_in+tokens_out),0) FROM jobs WHERE node_id=? AND status='complete' AND date(started_at)=date('now')`, nodeID).Scan(&jobs, &tokens)
	return
}

// ValidateUserKey checks if a user API key exists and returns remaining daily quota.
func (d *DB) ValidateUserKey(key string) (userID string, remaining int, err error) {
	var quotaDaily int
	err = d.db.QueryRow(`SELECT user_id, quota_daily FROM api_keys WHERE key=?`, key).Scan(&userID, &quotaDaily)
	if err != nil {
		return "", 0, err
	}
	var usedToday int
	d.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE user_key=? AND date(started_at)=date('now')`, key).Scan(&usedToday)
	remaining = quotaDaily - usedToday
	return userID, remaining, nil
}

// CreateUserKey inserts a new user API key.
func (d *DB) CreateUserKey(key, userID, name string, quotaDaily int) error {
	_, err := d.db.Exec(`INSERT INTO api_keys (key, user_id, name, quota_daily) VALUES (?, ?, ?, ?)`,
		key, userID, name, quotaDaily)
	return err
}

// UserBalance returns the balance in msats for a user API key.
func (d *DB) UserBalance(key string) (int64, error) {
	var v sql.NullInt64
	err := d.db.QueryRow(`SELECT balance_msats FROM api_keys WHERE key=?`, key).Scan(&v)
	return v.Int64, err
}

// DeductBalance atomically deducts msats from a user's balance.
// Returns error if insufficient balance.
func (d *DB) DeductBalance(key string, amountMsats int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var current int64
	if err := tx.QueryRow(`SELECT balance_msats FROM api_keys WHERE key=?`, key).Scan(&current); err != nil {
		return err
	}
	if current < amountMsats {
		return fmt.Errorf("insufficient balance: %d < %d msats", current, amountMsats)
	}
	if _, err := tx.Exec(`UPDATE api_keys SET balance_msats = balance_msats - ? WHERE key=?`, amountMsats, key); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateInvoice stores a pending Lightning invoice for a user top-up.
func (d *DB) CreateInvoice(paymentHash, userKey string, amountMsats int64, bolt11 string) error {
	_, err := d.db.Exec(`INSERT INTO invoices (payment_hash, user_key, amount_msats, bolt11, status) VALUES (?, ?, ?, ?, 'pending')`,
		paymentHash, userKey, amountMsats, bolt11)
	return err
}

// CreditInvoice atomically marks an invoice as credited and adds to user balance.
// Returns false if already credited (idempotent).
func (d *DB) CreditInvoice(paymentHash string) (credited bool, err error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var status string
	var userKey string
	var amountMsats int64
	err = tx.QueryRow(`SELECT status, user_key, amount_msats FROM invoices WHERE payment_hash=?`, paymentHash).Scan(&status, &userKey, &amountMsats)
	if err != nil {
		return false, err
	}
	if status != "pending" {
		return false, nil // already credited or expired
	}
	if _, err := tx.Exec(`UPDATE invoices SET status='credited' WHERE payment_hash=?`, paymentHash); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE api_keys SET balance_msats = balance_msats + ? WHERE key=?`, amountMsats, userKey); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// PendingInvoices returns all invoices with status 'pending'.
func (d *DB) PendingInvoices() ([]Invoice, error) {
	rows, err := d.db.Query(`SELECT payment_hash, user_key, amount_msats, bolt11, created_at FROM invoices WHERE status='pending'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(&inv.PaymentHash, &inv.UserKey, &inv.AmountMsats, &inv.Bolt11, &inv.CreatedAt); err != nil {
			continue
		}
		out = append(out, inv)
	}
	return out, nil
}

// ExpireOldInvoices marks invoices older than 10 minutes as expired.
func (d *DB) ExpireOldInvoices() (int64, error) {
	r, err := d.db.Exec(`UPDATE invoices SET status='expired' WHERE status='pending' AND created_at < datetime('now', '-10 minutes')`)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// Invoice represents a Lightning invoice for user top-up.
type Invoice struct {
	PaymentHash string
	UserKey     string
	AmountMsats int64
	Bolt11      string
	CreatedAt   time.Time
}

// AuditJob is an anonymised job record for the public audit log.
type AuditJob struct {
	Model       string `json:"model"`
	TokensIn    int    `json:"tokens_in"`
	TokensOut   int    `json:"tokens_out"`
	EarnedMsats int64  `json:"earned_msats"`
	FeeMsats    int64  `json:"fee_msats"`
	Timestamp   string `json:"timestamp"`
}

// RecentJobs returns the last N completed jobs, anonymised (no user keys or node IDs).
func (d *DB) RecentJobs(limit int) ([]AuditJob, error) {
	rows, err := d.db.Query(`SELECT model, tokens_in, tokens_out, earned_msats, fee_msats, started_at FROM jobs WHERE status='complete' ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditJob
	for rows.Next() {
		var j AuditJob
		if err := rows.Scan(&j.Model, &j.TokensIn, &j.TokensOut, &j.EarnedMsats, &j.FeeMsats, &j.Timestamp); err != nil {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}

// NodeKarma returns free tier jobs served and a karma score for a node.
// Karma = free_tier_jobs * 10 + total_jobs (last 30 days).
func (d *DB) NodeKarma(nodeID string) (freeTierJobs int, karmaScore int64, err error) {
	err = d.db.QueryRow(`SELECT COALESCE(SUM(CASE WHEN free_tier THEN 1 ELSE 0 END), 0) FROM jobs WHERE node_id=? AND status='complete'`, nodeID).Scan(&freeTierJobs)
	if err != nil {
		return
	}
	var totalJobs30d int
	d.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE node_id=? AND status='complete' AND started_at >= datetime('now', '-30 days')`, nodeID).Scan(&totalJobs30d)
	karmaScore = int64(freeTierJobs*10 + totalJobs30d)
	return
}

// NodeWithdrawHistory returns the last N payouts for a node.
func (d *DB) NodeWithdrawHistory(nodeID string, limit int) ([]Payout, error) {
	rows, err := d.db.Query(`SELECT id, node_id, amount_msats, payment_hash, status, created_at FROM payouts WHERE node_id=? AND status='confirmed' ORDER BY created_at DESC LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payout
	for rows.Next() {
		var p Payout
		if err := rows.Scan(&p.ID, &p.NodeID, &p.AmountMsats, &p.PaymentHash, &p.Status, &p.CreatedAt); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// GlobalStats24h returns total jobs and tokens across all nodes in the last 24 hours.
func (d *DB) GlobalStats24h() (jobs int, tokens int, err error) {
	err = d.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(tokens_in+tokens_out),0) FROM jobs WHERE status='complete' AND started_at >= datetime('now', '-24 hours')`).Scan(&jobs, &tokens)
	return
}

// RecordPayout inserts a payout record.
func (d *DB) RecordPayout(nodeID string, amountMsats int64, paymentHash, status string) error {
	_, err := d.db.Exec(`INSERT INTO payouts (node_id, amount_msats, payment_hash, status, confirmed_at) VALUES (?, ?, ?, ?, ?)`,
		nodeID, amountMsats, paymentHash, status, time.Now().UTC())
	return err
}

// PendingPayouts returns nodes whose balance exceeds their redeem threshold.
func (d *DB) PendingPayouts() ([]Node, error) {
	rows, err := d.db.Query(`
		SELECT node_id, lightning_addr, redeem_threshold, balance FROM (
			SELECT n.node_id, n.lightning_addr, n.redeem_threshold,
				COALESCE((SELECT SUM(earned_msats) FROM jobs WHERE node_id=n.node_id AND status='complete'), 0) -
				COALESCE((SELECT SUM(amount_msats) FROM payouts WHERE node_id=n.node_id AND status IN ('sent','confirmed')), 0) AS balance
			FROM nodes n
			WHERE n.lightning_addr != ''
		) WHERE balance >= redeem_threshold * 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		var balance int64
		if err := rows.Scan(&n.NodeID, &n.LightningAddr, &n.RedeemThreshold, &balance); err != nil {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}
