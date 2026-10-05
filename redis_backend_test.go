package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func redisBackendFor(t *testing.T) (*RedisBackend, *miniredis.Miniredis) {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	return NewRedisBackend(client, ""), server
}

func TestRedisBackendThreshold(t *testing.T) {
	backend, _ := redisBackendFor(t)

	if !backend.Allow("ip:1", 2, time.Minute) {
		t.Fatalf("hit 1 should be allowed")
	}
	if !backend.Allow("ip:1", 2, time.Minute) {
		t.Fatalf("hit 2 should be allowed")
	}
	if backend.Allow("ip:1", 2, time.Minute) {
		t.Fatalf("hit 3 should be blocked")
	}

	// Other keys are unaffected.
	if !backend.Allow("ip:2", 2, time.Minute) {
		t.Fatalf("other key should be allowed")
	}
}

func TestRedisBackendWindowExpiry(t *testing.T) {
	backend, server := redisBackendFor(t)

	backend.Allow("ip:1", 1, time.Minute)
	if backend.Allow("ip:1", 1, time.Minute) {
		t.Fatalf("hit 2 should be blocked")
	}

	server.FastForward(61 * time.Second)

	if !backend.Allow("ip:1", 1, time.Minute) {
		t.Fatalf("hit after window expiry should be allowed")
	}
}

func TestRedisBackendKeysAreNamespaced(t *testing.T) {
	backend, server := redisBackendFor(t)

	backend.Allow("ip:1", 5, time.Minute)

	keys := server.Keys()
	if len(keys) != 1 || keys[0] != "ratelimit:60:ip:1" {
		t.Fatalf("unexpected keys: %v", keys)
	}
}

func TestRedisBackendReset(t *testing.T) {
	backend, _ := redisBackendFor(t)

	backend.Allow("ip:1", 1, time.Minute)
	if backend.Allow("ip:1", 1, time.Minute) {
		t.Fatalf("hit 2 should be blocked")
	}

	backend.Reset()

	if !backend.Allow("ip:1", 1, time.Minute) {
		t.Fatalf("hit after reset should be allowed")
	}
}

func TestRedisBackendFailOpen(t *testing.T) {
	backend, server := redisBackendFor(t)
	server.Close()

	if !backend.Allow("ip:1", 1, time.Minute) {
		t.Fatalf("broken Redis must fail open")
	}
}

func TestRedisBackendConcurrent(t *testing.T) {
	backend, _ := redisBackendFor(t)

	var wg sync.WaitGroup
	allowed := make(chan bool, 200)

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed <- backend.Allow("ip:1", 100, time.Minute)
		}()
	}
	wg.Wait()
	close(allowed)

	total := 0
	for ok := range allowed {
		if ok {
			total++
		}
	}
	if total != 100 {
		t.Fatalf("expected exactly 100 allowed, got %d", total)
	}
	// The counter records every attempt, not only the allowed ones.
	if got := backend.Count("ip:1", time.Minute); got != 200 {
		t.Fatalf("expected counter 200, got %d", got)
	}
}

func TestRedisBackendThroughMiddleware(t *testing.T) {
	backend, _ := redisBackendFor(t)
	limiter := NewLimiter(2, time.Minute, WithBackend(backend))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", limiter.Middleware(), func(c *gin.Context) { c.Status(http.StatusOK) })

	for i, want := range []int{200, 200, 429} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		if w.Code != want {
			t.Fatalf("request %d: expected %d, got %d", i+1, want, w.Code)
		}
	}
}
