package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setup(l *Limiter, pattern string) *gin.Engine {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET(pattern, l.Middleware(), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return r
}

func hit(r *gin.Engine, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestAllowsUpToMaxThenBlocks(t *testing.T) {
	r := setup(NewLimiter(2, time.Minute), "/search")

	for i := 1; i <= 2; i++ {
		if w := hit(r, "/search"); w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, w.Code)
		}
	}

	if w := hit(r, "/search"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("request 3: expected 429, got %d", w.Code)
	}
}

func TestWindowExpiry(t *testing.T) {
	r := setup(NewLimiter(1, 40*time.Millisecond), "/search")

	if w := hit(r, "/search"); w.Code != http.StatusOK {
		t.Fatalf("first: expected 200, got %d", w.Code)
	}
	if w := hit(r, "/search"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second: expected 429, got %d", w.Code)
	}

	time.Sleep(60 * time.Millisecond)

	if w := hit(r, "/search"); w.Code != http.StatusOK {
		t.Fatalf("after window: expected 200, got %d", w.Code)
	}
}

func TestCustomKeyFuncSeparatesBuckets(t *testing.T) {
	limiter := NewLimiter(1, time.Minute, WithKeyFunc(func(c *gin.Context) string {
		return "tenant:" + c.GetHeader("X-Tenant")
	}))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", limiter.Middleware(), func(c *gin.Context) { c.Status(http.StatusOK) })

	request := func(tenant string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("X-Tenant", tenant)
		r.ServeHTTP(w, req)
		return w
	}

	if w := request("a"); w.Code != http.StatusOK {
		t.Fatalf("tenant a first: %d", w.Code)
	}
	if w := request("a"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("tenant a second: expected 429, got %d", w.Code)
	}
	if w := request("b"); w.Code != http.StatusOK {
		t.Fatalf("tenant b first: expected 200, got %d", w.Code)
	}
}

func TestJSONTooManyHandler(t *testing.T) {
	limiter := NewLimiter(1, time.Minute,
		WithTooManyHandler(JSONTooMany("rate_limited", "too many requests")))

	r := setup(limiter, "/api/v1/x")
	hit(r, "/api/v1/x")

	w := hit(r, "/api/v1/x")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"rate_limited"`) {
		t.Fatalf("expected JSON envelope, got %s", w.Body.String())
	}
}

func TestSharedBackendCombinesBudgets(t *testing.T) {
	backend := NewMemoryBackend()
	api := NewLimiter(2, time.Minute, WithBackend(backend),
		WithTooManyHandler(JSONTooMany("rate_limited", "slow down")))
	page := NewLimiter(2, time.Minute, WithBackend(backend))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/x", api.Middleware(), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/page", page.Middleware(), func(c *gin.Context) { c.Status(http.StatusOK) })

	hit(r, "/api/x")
	hit(r, "/page")

	// The third hit across both routes exceeds the shared budget of 2 and is
	// answered with the route-specific rejection response.
	if w := hit(r, "/page"); w.Code != http.StatusTooManyRequests ||
		strings.Contains(w.Body.String(), "rate_limited") {
		t.Fatalf("page route should get the plain 429, got %d %s", w.Code, w.Body.String())
	}
	if w := hit(r, "/api/x"); !strings.Contains(w.Body.String(), "rate_limited") {
		t.Fatalf("api route should get the JSON envelope, got %s", w.Body.String())
	}
}

func TestReset(t *testing.T) {
	limiter := NewLimiter(1, time.Minute)
	r := setup(limiter, "/search")

	hit(r, "/search")
	if w := hit(r, "/search"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}

	limiter.Reset()

	if w := hit(r, "/search"); w.Code != http.StatusOK {
		t.Fatalf("after reset: expected 200, got %d", w.Code)
	}
}

func TestConcurrentSafety(t *testing.T) {
	limiter := NewLimiter(100, time.Minute)
	r := setup(limiter, "/search")

	var wg sync.WaitGroup
	allowed := make(chan int, 200)

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := hit(r, "/search"); w.Code == http.StatusOK {
				allowed <- 1
			}
		}()
	}
	wg.Wait()
	close(allowed)

	total := 0
	for range allowed {
		total++
	}
	if total != 100 {
		t.Fatalf("expected exactly 100 allowed, got %d", total)
	}
}
