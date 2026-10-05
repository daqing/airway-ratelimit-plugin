// Package ratelimit is a Gin middleware library for fixed-window rate
// limiting, built for Airway applications but usable in any Gin server.
//
// A Limiter bundles a rule (max requests per window), a key strategy and the
// response for rejected requests. Several limiters may share one Backend to
// enforce a single budget across routes with different rejection responses.
//
//	limiter := ratelimit.NewLimiter(60, time.Minute)
//	r.GET("/search", limiter.Middleware(), handler)
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// KeyFunc extracts the bucket key for a request (per IP, per user, …).
type KeyFunc func(*gin.Context) string

// ByIP keys requests by client IP.
func ByIP(c *gin.Context) string {
	return "ip:" + c.ClientIP()
}

// Backend stores the request counters. Implementations must be safe for
// concurrent use.
type Backend interface {
	// Allow records one hit for key and reports whether it stays within max
	// hits per window.
	Allow(key string, max int, window time.Duration) bool
	// Reset clears all counters.
	Reset()
}

// Limiter is one rate-limiting rule. Construct it with NewLimiter and mount
// Middleware on routes.
type Limiter struct {
	max     int
	window  time.Duration
	keyFn   KeyFunc
	backend Backend
	tooMany gin.HandlerFunc
}

// Option customizes a Limiter.
type Option func(*Limiter)

// WithKeyFunc sets the bucket key strategy. Default: ByIP.
func WithKeyFunc(fn KeyFunc) Option {
	return func(l *Limiter) {
		l.keyFn = fn
	}
}

// WithBackend sets the counter store. Default: a new in-memory backend.
// Share one backend between limiters to enforce a combined budget across
// routes.
func WithBackend(b Backend) Option {
	return func(l *Limiter) {
		l.backend = b
	}
}

// WithTooManyHandler sets the response for rejected requests. Default: a
// plain-text 429.
func WithTooManyHandler(h gin.HandlerFunc) Option {
	return func(l *Limiter) {
		l.tooMany = h
	}
}

// JSONTooMany replies with an error envelope, e.g. for API routes:
//
//	ratelimit.WithTooManyHandler(ratelimit.JSONTooMany("rate_limited", "slow down"))
func JSONTooMany(code, message string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": gin.H{"code": code, "message": message},
		})
	}
}

// NewLimiter builds a limiter allowing max requests per window.
func NewLimiter(max int, window time.Duration, opts ...Option) *Limiter {
	l := &Limiter{
		max:     max,
		window:  window,
		keyFn:   ByIP,
		backend: NewMemoryBackend(),
		tooMany: func(c *gin.Context) {
			c.AbortWithStatus(http.StatusTooManyRequests)
		},
	}

	for _, opt := range opts {
		opt(l)
	}

	return l
}

// New is shorthand for NewLimiter(...).Middleware().
func New(max int, window time.Duration, opts ...Option) gin.HandlerFunc {
	return NewLimiter(max, window, opts...).Middleware()
}

// Middleware returns the Gin middleware enforcing the rule.
func (l *Limiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.backend.Allow(l.keyFn(c), l.max, l.window) {
			l.tooMany(c)
			return
		}
		c.Next()
	}
}

// Reset clears all counters of the limiter's backend. For tests.
func (l *Limiter) Reset() {
	l.backend.Reset()
}

// MemoryBackend is an in-process fixed-window Backend.
type MemoryBackend struct {
	mu      sync.Mutex
	windows map[string]*memoryWindow
	now     func() time.Time
}

type memoryWindow struct {
	count   int
	resetAt time.Time
}

// NewMemoryBackend returns an empty in-memory backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{windows: map[string]*memoryWindow{}, now: time.Now}
}

func (b *MemoryBackend) Allow(key string, max int, window time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	w, ok := b.windows[key]
	if !ok || !now.Before(w.resetAt) {
		b.windows[key] = &memoryWindow{count: 1, resetAt: now.Add(window)}
		return true
	}

	w.count++
	return w.count <= max
}

func (b *MemoryBackend) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.windows = map[string]*memoryWindow{}
}
