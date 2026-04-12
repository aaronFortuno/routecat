// Package gateway handles WebSocket connections from provider nodes.
// It implements the Owlrun-compatible control protocol: registration,
// heartbeat, job assignment, and proxy streaming.
package gateway

// RegisterPayload is the JSON body of POST /v1/gateway/register.
// Wire-compatible with the Owlrun client.
type RegisterPayload struct {
	NodeID           string   `json:"node_id"`
	APIKey           string   `json:"api_key"`
	GPU              string   `json:"gpu"`
	GPUVendor        string   `json:"gpu_vendor"`
	VRAMTotalMB      int      `json:"vram_total_mb"`
	VRAMFreeMB       int      `json:"vram_free_mb"`
	VRAMExact        bool     `json:"vram_exact"`
	Models           []string `json:"models"`
	OllamaURL        string   `json:"ollama_url"`
	Region           string   `json:"region"`
	Wallet           string   `json:"wallet,omitempty"`
	ReferralCode     string   `json:"referral_code,omitempty"`
	LightningAddress string   `json:"lightning_address,omitempty"`
	RedeemThreshold  int      `json:"redeem_threshold_sats,omitempty"`
	FreeTierPct      int      `json:"free_tier_pct,omitempty"`
	Version          string   `json:"version"`
}

// WSMsg is the generic WebSocket message envelope.
// Wire-compatible with the Owlrun client.
type WSMsg struct {
	Type string `json:"type"`

	// Identity
	NodeID string `json:"node_id,omitempty"`

	// Job assignment (gateway -> node)
	JobID          string `json:"job_id,omitempty"`
	Model          string `json:"model,omitempty"`
	VRAMRequiredMB int    `json:"vram_required_mb,omitempty"`
	BuyerRegion    string `json:"buyer_region,omitempty"`
	FreeTier       bool   `json:"free_tier,omitempty"`

	// Heartbeat (node -> gateway)
	GPUUtilPct   int     `json:"gpu_util_pct,omitempty"`
	VRAMFreeMB   int     `json:"vram_free_mb,omitempty"`
	TempC        int     `json:"temp_c,omitempty"`
	PowerW       float64 `json:"power_w,omitempty"`
	QueueDepth   int     `json:"queue_depth,omitempty"`
	EarningState string  `json:"earning_state,omitempty"`

	// Heartbeat ACK (gateway -> node)
	Status           string      `json:"status,omitempty"`
	JobsToday        int         `json:"jobs_today,omitempty"`
	TokensToday      int         `json:"tokens_today,omitempty"`
	EarnedTodayUSD   float64     `json:"earned_today_usd,omitempty"`
	EarnedTodaySats  int64       `json:"earned_today_sats,omitempty"`
	EarnedTotalSats  int64       `json:"earned_total_sats,omitempty"`
	QueueDepthGlobal int         `json:"queue_depth_global,omitempty"`
	BalanceSats      int64       `json:"balance_sats,omitempty"`
	KarmaScore       int64       `json:"karma_score,omitempty"`
	KarmaTier        string      `json:"karma_tier,omitempty"`
	FreeTierJobs     int         `json:"free_tier_jobs,omitempty"`
	Broadcasts       []Broadcast `json:"broadcasts,omitempty"`
	WithdrawHistory  []Withdraw  `json:"withdraw_history,omitempty"`

	// BTC price (in heartbeat ACK)
	BtcLiveUsd      float64 `json:"btc_live_usd,omitempty"`
	BtcYesterdayFix float64 `json:"btc_yesterday_fix,omitempty"`
	BtcDailyAvg     float64 `json:"btc_daily_avg,omitempty"`
	BtcWeeklyAvg    float64 `json:"btc_weekly_avg,omitempty"`
	BtcPriceStatus  string  `json:"btc_price_status,omitempty"`

	// Job complete (gateway -> node)
	Tokens    int     `json:"tokens,omitempty"`
	EarnedUSD float64 `json:"earned_usd,omitempty"`

	// Reject reason (node -> gateway)
	Reason string `json:"reason,omitempty"`

	// Proxy streaming (node -> gateway)
	Data string `json:"data,omitempty"`
}

// Broadcast is a gateway notification shown on provider dashboards.
type Broadcast struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Severity  string `json:"severity"`
	Timestamp string `json:"created_at"`
}

// Withdraw is a Lightning payout record.
type Withdraw struct {
	AmountSats  int64  `json:"amount_sats"`
	PaymentHash string `json:"payment_hash"`
	Timestamp   string `json:"timestamp"`
}
