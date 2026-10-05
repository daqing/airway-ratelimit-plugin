# airway-ratelimit-plugin

Fixed-window rate-limiting middleware for Gin, built for
[Airway](https://github.com/daqing/airway) applications but usable in any Gin
server.

[中文](README.zh-CN.md)

## Usage

```go
import "github.com/daqing/airway-ratelimit-plugin"

// 60 requests per minute per client IP, plain-text 429 on rejection.
search := ratelimit.NewLimiter(60, time.Minute)
r.GET("/search", search.Middleware(), SearchAction)

// Shorthand when you don't need the Limiter handle:
r.GET("/search", ratelimit.New(60, time.Minute), SearchAction)
```

Per-authenticated-user limiting (mount after your auth middleware):

```go
publish := ratelimit.NewLimiter(30, time.Hour,
    ratelimit.WithKeyFunc(func(c *gin.Context) string {
        return "user:" + currentUserID(c)
    }),
    ratelimit.WithTooManyHandler(ratelimit.JSONTooMany("rate_limited", "too many requests")),
)
r.POST("/api/v1/packages", auth, publish.Middleware(), PublishAction)
```

## API

- `NewLimiter(max, window, opts...) *Limiter` / `New(max, window, opts...) gin.HandlerFunc`
- `(*Limiter).Middleware() gin.HandlerFunc`, `(*Limiter).Reset()` (tests)
- Options:
  - `WithKeyFunc(fn KeyFunc)` — bucket key strategy; built-in `ByIP` (default)
  - `WithBackend(b Backend)` — counter store; default `NewMemoryBackend()`
  - `WithTooManyHandler(h gin.HandlerFunc)` — rejection response; default
    plain-text 429, `JSONTooMany(code, message)` ships an error envelope

## Shared budgets

Limiters sharing one `Backend` draw from the same counter, so a route pair can
enforce a combined budget while keeping per-route rejection responses:

```go
backend := ratelimit.NewMemoryBackend()
api  := ratelimit.NewLimiter(10, time.Minute, ratelimit.WithBackend(backend),
        ratelimit.WithTooManyHandler(ratelimit.JSONTooMany("rate_limited", "slow down")))
page := ratelimit.NewLimiter(10, time.Minute, ratelimit.WithBackend(backend))
```

## Backends

The default memory backend is per-process state: right for a single node, not
a distributed limiter. Implement the two-method `Backend` interface
(`Allow(key, max, window) bool`, `Reset()`) to plug in another store.

### Redis backend

`NewRedisBackend(client, prefix)` ships a Redis backend (go-redis v9): counters
live at `<prefix>:<window seconds>:<key>` (default prefix `ratelimit`), bumped
by an atomic INCR + EXPIRE Lua script. It fails open when Redis is unreachable.

```go
rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
backend := ratelimit.NewRedisBackend(rdb, "ratelimit")

api  := ratelimit.NewLimiter(10, time.Minute, ratelimit.WithBackend(backend), ...)
page := ratelimit.NewLimiter(10, time.Minute, ratelimit.WithBackend(backend)) // shared budget
```

`(*RedisBackend).Reset()` deletes every key under the prefix (tests and ops).

Note: rules that share a backend, a window length **and** a key extractor share
one counter. That is the point for route pairs enforcing a combined budget, but
independent rules must namespace their keys, e.g. `WithKeyFunc(func(c) string {
return "search:" + ratelimit.ByIP(c) })`.

## Development

```bash
go test ./...
```
