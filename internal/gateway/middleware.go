package gateway

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter is a simple per-IP rate limiter.
type RateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int           // max requests
	window   time.Duration // time window
}

// NewRateLimiter creates a rate limiter.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
	// Cleanup stale entries every minute
	go func() {
		for range time.NewTicker(time.Minute).C {
			rl.cleanup()
		}
	}()
	return rl
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Remove expired entries
	reqs := rl.requests[ip]
	valid := reqs[:0]
	for _, t := range reqs {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= rl.limit {
		rl.requests[ip] = valid
		return false
	}

	rl.requests[ip] = append(valid, now)
	return true
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-rl.window)
	for ip, reqs := range rl.requests {
		valid := reqs[:0]
		for _, t := range reqs {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		if len(valid) == 0 {
			delete(rl.requests, ip)
		} else {
			rl.requests[ip] = valid
		}
	}
}

// Middleware wraps an HTTP handler with security protections.
func SecurityMiddleware(rl *RateLimiter, maxBodyBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Rate limiting
		// Use RemoteAddr only — X-Forwarded-For is set by Caddy but
		// could be spoofed if accessed directly. Caddy sets X-Real-Ip.
		ip := r.RemoteAddr
		if realIP := r.Header.Get("X-Real-Ip"); realIP != "" {
			ip = realIP
		}
		if !rl.Allow(ip) {
			http.Error(w, `{"error":{"message":"rate limit exceeded"}}`, http.StatusTooManyRequests)
			return
		}

		// Request body size limit
		if r.Body != nil && maxBodyBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}

		// Security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")

		next.ServeHTTP(w, r)
	})
}
