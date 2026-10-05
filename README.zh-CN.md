# airway-ratelimit

为 [Airway](https://github.com/daqing/airway) 应用打造的固定窗口限流 Gin
中间件,也可用于任何 Gin 服务。

[English](README.md)

## 用法

```go
import "github.com/daqing/airway-ratelimit"

// 每个客户端 IP 每分钟 60 次,超限返回纯文本 429。
search := ratelimit.NewLimiter(60, time.Minute)
r.GET("/search", search.Middleware(), SearchAction)

// 不需要 Limiter 句柄时的简写:
r.GET("/search", ratelimit.New(60, time.Minute), SearchAction)
```

按登录用户限流(挂在你的认证中间件之后):

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
- `(*Limiter).Middleware() gin.HandlerFunc`、`(*Limiter).Reset()`(测试用)
- 选项:
  - `WithKeyFunc(fn KeyFunc)`——计数桶 key 策略;内置 `ByIP`(默认)
  - `WithBackend(b Backend)`——计数存储;默认 `NewMemoryBackend()`
  - `WithTooManyHandler(h gin.HandlerFunc)`——超限响应;默认纯文本 429,
    `JSONTooMany(code, message)` 返回错误信封

## 共享配额

多个 limiter 共用一个 `Backend` 时共享同一计数器:两条路由可以执行合并配额,
同时保留各自的超限响应:

```go
backend := ratelimit.NewMemoryBackend()
api  := ratelimit.NewLimiter(10, time.Minute, ratelimit.WithBackend(backend),
        ratelimit.WithTooManyHandler(ratelimit.JSONTooMany("rate_limited", "slow down")))
page := ratelimit.NewLimiter(10, time.Minute, ratelimit.WithBackend(backend))
```

## 后端

默认内存后端是进程内状态:适合单节点,不是分布式限流器。实现两个方法的
`Backend` 接口(`Allow(key, max, window) bool`、`Reset()`)即可接入 Redis
或其他存储。

## 开发

```bash
go test ./...
```
