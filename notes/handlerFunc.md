# Go `http.HandlerFunc` Notes

An adapter in software engineering does exactly what a physical adapter does in the real world: it allows two incompatible things to connect and work together without modifying either of them.

## Adapter pattern view

`http.HandlerFunc` is a textbook adapter in the Go standard library. The
problem is that the HTTP server wants a value that satisfies the `http.Handler`
interface, but application code is most naturally written as a plain function:

```go
func(w http.ResponseWriter, r *http.Request) {
	// business logic
}
```

That function has the right runtime shape for a request callback, but it does
not have the method set of `http.Handler`.

The adapter solves this by wrapping the function in a type that exposes the
required method:

```go
type HandlerFunc func(http.ResponseWriter, *http.Request)

func (f HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f(w, r)
}
```

So the adapter pattern here is:

- the target interface is `http.Handler`
- the incompatible source shape is `func(http.ResponseWriter, *http.Request)`
- `HandlerFunc` implements the missing method and forwards execution to the
  function body

This lets the server keep a uniform interface while route authors keep a
simple function-first API.

In Go’s standard library, the HTTP server API is built around the
`http.Handler` interface:

```go
type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}
```

Any type that implements `ServeHTTP(w, r)` can participate in the HTTP
pipeline. That interface is the root of the server architecture.

## The adapter problem

A normal route function in Go is usually written as a function value:

```go
func(w http.ResponseWriter, r *http.Request) {
	// business logic
}
```

That function shape is exactly what the handler interface needs, but it does
not automatically satisfy the interface because methods are different from free
functions. To bridge that gap, Go ships the adapter type `http.HandlerFunc`.

## What `HandlerFunc` is

`http.HandlerFunc` is a defined type whose underlying type is a function with
this exact signature:

```go
func(http.ResponseWriter, *http.Request)
```

Its method is:

```go
type HandlerFunc func(ResponseWriter, *Request)

func (f HandlerFunc) ServeHTTP(w ResponseWriter, r *Request) {
	f(w, r)
}
```

The key line is the method receiver:

```go
func (f HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

That method makes a plain function satisfy the `Handler` interface. So a value
of type `http.HandlerFunc` is assignable to a `http.Handler`.

## Why this matters

Because of this adapter:

```go
http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("hello"))
})
```

`http.HandleFunc` is a convenience wrapper over `http.Handle` that accepts a
plain function and converts it to a `HandlerFunc` internally.

The adaptation is usually done something like this:

```go
func HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	DefaultServeMux.Handle(pattern, HandlerFunc(handler))
}
```

So the route registration API lets application code write ordinary functions,
then adapt them to the interface contract automatically.

## How it satisfies the interface

This is a compile-time interface satisfaction story:

```go
var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
})
```

The right-hand side is a function value, but its type is `http.HandlerFunc`.
Because `HandlerFunc` has a `ServeHTTP` method, it satisfies `http.Handler`.
The method simply forwards the call to the underlying function body.

That is why a middleware can also be built around `http.HandlerFunc`.

## Middleware relationship

Middleware in Go usually returns a `http.Handler` or `http.HandlerFunc`.
Examples:

```go
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start))
	})
}
```

The returned wrapper is a `HandlerFunc` instance, so it satisfies the same
interface contract. That is exactly the same adapter mechanism at work:

- the middleware is a function-shaped handler
- `http.HandlerFunc` adapts it to the `http.Handler` interface
- the outer HTTP server can store that wrapper in the chain

## Practical meaning

`http.HandlerFunc` is the conversion bridge between two layers:

- business-level route functions are written as interfaces-free functions
- the HTTP stack expects `http.Handler`

So `HandlerFunc` is not a different runtime concept; it is a convenience type
that lets ordinary functions participate in the handler contract.

## Key takeaway

`http.HandlerFunc` is the standard library adapter that says:

- “If you have a function with signature `func(http.ResponseWriter, *http.Request)`,
  treat it as a `http.Handler` by implementing `ServeHTTP()` as a one-line call
  to that function.”

That is why the net/http API is so ergonomic: route code can stay function-based
while the framework stores the chain as interface values.
