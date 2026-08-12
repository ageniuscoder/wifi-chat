# Creating Your Own Types in Go

The general syntax is:

```go
type TypeName ExistingType
```

Example:

```go
type MessageType string
```

Here, `MessageType` is a new distinct type whose underlying type is `string`. It is not exactly the same as `string`.

## Why this matters

Go lets you define your own types from existing ones.

```go
type MessageType string
var msgType MessageType
msgType = "text"
```

That works because the string literal `"text"` is an untyped constant and Go can assign it to a string-based custom type.

## What fails

```go
var s string = "text"
var m MessageType = s // compile error
```

This fails because:

- `s` has type `string`
- `m` has type `MessageType`
- `string` and `MessageType` are different named types

You must convert explicitly:

```go
var s string = "text"
var m MessageType = MessageType(s)
```

That conversion is valid because the underlying type is the same.

## Common Go style

The following pattern is very common in Go codebases:

```go
type MessageType string

const (
    MessageText  MessageType = "text"
    MessageImage MessageType = "image"
    MessageVideo MessageType = "video"
)
```

This is useful because the constants are strongly typed and can be checked in switches or validated against a defined domain.

## Defining methods on a custom type

```go
type MessageType string

const (
    MessageText  MessageType = "text"
    MessageImage MessageType = "image"
)

func (m MessageType) IsValid() bool {
    switch m {
    case MessageText, MessageImage:
        return true
    default:
        return false
    }
}
```

This makes it easy to attach behavior to the type instead of treating it as a plain string.

## Important distinction

### Distinct type

```go
type MessageType string
```

This creates a new distinct type. It is not interchangeable with `string` without conversion.

### Type alias

```go
type MessageType = string
```

This is a type alias. It does not create a new named type; it gives `string` another name.

Because of that:

```go
type MessageType = string
var s string = "text"
var m MessageType = s
```

This is valid because `MessageType` and `string` are the same type.

## Summary

- `type MessageType string` → new distinct type based on `string`
- `type MessageType = string` → alias, not a new type
- Unstructured string literals can usually be assigned to a string-backed custom type
- Assigning a `string` variable to a custom string type requires an explicit conversion

This is the foundation for safer domain modeling in Go codebases such as chat message kinds, routing types, and API enums.
