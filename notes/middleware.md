# Go HTTP Middleware Notes

Middleware in Go is a request-processing layer that sits between an incoming
HTTP request and the final application handler. It usually wraps the next
handler and can:

- inspect or validate the request
- add headers or context values
- perform logging, tracing, or metrics
- short-circuit the request and return a response directly
- call `next.ServeHTTP()` to continue the chain

A middleware function normally follows this shape:

```go
func MyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. PRE-PROCESSING
		// Example: check headers, add request ID, start timer

		next.ServeHTTP(w, r)

		// 2. POST-PROCESSING
		// Example: log timing or enrich the response
	})
}
```

The important idea is that middleware is a decorator around the actual HTTP
handler. The middleware receives the original `http.ResponseWriter` and
`*http.Request`, then either forwards them or stops the request.

## Middleware chaining

Go’s standard library uses the `http.Handler` interface:

```go
type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}
```

This interface is the contract for every HTTP layer. Middleware implementations
usually return a `http.Handler` or `http.HandlerFunc` that wraps another handler.

A simple chain looks like this:

```go
http.Handle("/", loggingMiddleware(finalHandler))
```

Or with nested composition:

```go
handler := loggingMiddleware(authMiddleware(finalHandler))
server := &http.Server{Handler: handler}
```

The stack is layered like a nested function call:

- the outer middleware runs first
- it decides whether to call the next middleware
- the inner handler eventually responds
- the control flows back outward after the response is prepared

## Request lifecycle

A request enters the server through `http.Server` and reaches the top-level
handler chain. A middleware can participate in three phases:

1. Before the request reaches the business logic
2. During request processing
3. After the final handler has produced a response

That makes middleware useful for cross-cutting concerns such as:

- logging
- authentication and authorization
- rate limiting
- CORS headers
- request timeouts
- panic recovery
- tracing IDs

## Short-circuiting

A middleware may choose not to call the next handler at all. This is how it
implements policies such as:

- rejecting missing authentication
- blocking invalid or suspicious input
- serving a cached response

Example:

```go
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

This pattern is a clean way to centralize access checks without putting those
checks into each route.

## Context propagation

Middleware can enrich the request context:

```go
ctx := context.WithValue(r.Context(), "user", "alice")
r = r.WithContext(ctx)
next.ServeHTTP(w, r)
```

The downstream handler can later read the same context values. This is the
standard way to pass request-scoped information such as user identity, trace
IDs, or permissions through the HTTP stack.

## Logging middleware example

A logging middleware is one of the simplest and most common examples:

```go
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start))
	})
}
```

This middleware does not replace the application handler. It observes the
request lifecycle and records timing around the inner handler.

## Panic recovery middleware

A recovery middleware protects the server from panics in downstream handlers:

```go
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
```

This is a common resilience middleware because it makes the HTTP server return
an error instead of crashing the process when a later layer panics.

## In this repository

The server in this project uses a logging wrapper around the HTTP mux in
[internal/server/server.go](internal/server/server.go#L151-L163):

- `loggingMiddleware()` records the request path and elapsed time
- it only logs API and WebSocket paths
- it calls `next.ServeHTTP()` to continue the chain

This is a textbook example of middleware as a cross-cutting concern instead of
business logic.

## Key takeaways

- Middleware is a `http.Handler` wrapper that decorates a final handler.
- It can execute before, during, or after the downstream handler.
- It is used for logging, auth, tracing, recovery, metrics, rate limiting, and
  request shaping.
- It may continue the chain with `next.ServeHTTP()` or terminate it early.
- The request flows through a nested chain rather than through a single global
  object.
