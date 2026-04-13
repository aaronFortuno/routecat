// Package billing meters tokens from inference jobs and calculates
// provider earnings with a transparent fee structure.
package billing

import (
	"github.com/aaronFortuno/routecat/internal/store"
)

// ModelPricing holds per-model pricing (per million tokens).
type ModelPricing struct {
	PerMInputUSD  float64 `json:"per_m_input_usd"`
	PerMOutputUSD float64 `json:"per_m_output_usd"`
}

// Engine calculates earnings and fees for completed jobs.
type Engine struct {
	db     *store.DB
	feePct float64 // gateway fee percentage (e.g. 5.0 for 5%)
	prices *PriceTracker

	// pricing is loaded at startup and refreshed periodically.
	pricing map[string]ModelPricing // model tag -> pricing
}

// New creates a billing engine with the given fee percentage.
func New(db *store.DB, feePct float64) *Engine {
	return &Engine{
		db:      db,
		feePct:  feePct,
		prices:  NewPriceTracker(),
		pricing: defaultPricing(),
	}
}

// BtcPrice returns the current BTC/USD price.
func (e *Engine) BtcPrice() float64 { return e.prices.Price() }

// FeePct returns the current gateway fee percentage.
func (e *Engine) FeePct() float64 { return e.feePct }

// Calculate computes earnings for a completed job.
// Returns: grossUSD, providerMsats, feeMsats.
func (e *Engine) Calculate(model string, tokensIn, tokensOut int, btcPriceUSD float64) (grossUSD float64, providerMsats int64, feeMsats int64) {
	p, ok := e.pricing[model]
	if !ok {
		// Fallback: use a conservative default
		p = ModelPricing{PerMInputUSD: 0.0005, PerMOutputUSD: 0.001}
	}

	grossUSD = float64(tokensIn)/1_000_000*p.PerMInputUSD + float64(tokensOut)/1_000_000*p.PerMOutputUSD

	if btcPriceUSD <= 0 {
		return grossUSD, 0, 0
	}

	// Convert USD to msats: 1 BTC = 100_000_000 sats = 100_000_000_000 msats
	totalMsats := int64(grossUSD / btcPriceUSD * 100_000_000_000)
	feeMsats = int64(float64(totalMsats) * e.feePct / 100)
	providerMsats = totalMsats - feeMsats
	return grossUSD, providerMsats, feeMsats
}

// GetPricing returns pricing for a model, or nil if unknown.
func (e *Engine) GetPricing(model string) *ModelPricing {
	p, ok := e.pricing[model]
	if !ok {
		return nil
	}
	return &p
}

// AllPricing returns the full pricing map.
func (e *Engine) AllPricing() map[string]ModelPricing {
	return e.pricing
}

// defaultPricing returns pricing for common models.
// Strategy: slightly below Owlrun's user-facing prices, but with a fixed
// 95% provider share (vs Owlrun's 91-96% variable), making it more
// attractive for nodes while still cheaper for users.
func defaultPricing() map[string]ModelPricing {
	return map[string]ModelPricing{
		// Large / premium models (Owlrun: ~$0.12-0.15 output)
		"llama3.1:70b":         {PerMInputUSD: 0.10, PerMOutputUSD: 0.14},
		"qwen2.5:32b":          {PerMInputUSD: 0.06, PerMOutputUSD: 0.11},
		"mixtral:8x7b":         {PerMInputUSD: 0.06, PerMOutputUSD: 0.11},
		"command-r:35b":        {PerMInputUSD: 0.05, PerMOutputUSD: 0.10},
		"gemma2:27b":           {PerMInputUSD: 0.05, PerMOutputUSD: 0.10},
		"qwen2.5:14b":          {PerMInputUSD: 0.04, PerMOutputUSD: 0.11},
		"deepseek-r1:14b":      {PerMInputUSD: 0.05, PerMOutputUSD: 0.11},
		// Mid-range models (Owlrun: ~$0.05-0.08 output)
		"llama3.1:8b":          {PerMInputUSD: 0.03, PerMOutputUSD: 0.07},
		"qwen2.5:7b":           {PerMInputUSD: 0.03, PerMOutputUSD: 0.07},
		"deepseek-r1:7b":       {PerMInputUSD: 0.03, PerMOutputUSD: 0.07},
		"gemma2:9b":            {PerMInputUSD: 0.03, PerMOutputUSD: 0.07},
		"mistral:7b":           {PerMInputUSD: 0.03, PerMOutputUSD: 0.07},
		"phi4-mini:latest":     {PerMInputUSD: 0.02, PerMOutputUSD: 0.06},
		// Small models (Owlrun: ~$0.02-0.03 output)
		"llama3.2:3b":          {PerMInputUSD: 0.015, PerMOutputUSD: 0.04},
		"qwen2.5:3b":           {PerMInputUSD: 0.015, PerMOutputUSD: 0.04},
		"qwen2.5:1.5b":         {PerMInputUSD: 0.008, PerMOutputUSD: 0.02},
		"phi3:mini":            {PerMInputUSD: 0.015, PerMOutputUSD: 0.04},
		// Tiny / fast models
		"qwen2.5:0.5b":         {PerMInputUSD: 0.005, PerMOutputUSD: 0.01},
		"tinyllama:1.1b":       {PerMInputUSD: 0.003, PerMOutputUSD: 0.008},
	}
}

// FallbackPricing returns the default pricing for unknown models.
func FallbackPricing() ModelPricing {
	return ModelPricing{PerMInputUSD: 0.02, PerMOutputUSD: 0.06}
}
