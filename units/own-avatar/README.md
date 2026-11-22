# own-avatar

An HTTP handler that serves the signed-in user's own photo, for a profile head or a bottom menu. It takes no handle and no
user id from the request: the handle is made inside the handler from the user the caller names, so no other user's photo is
reachable through it. The photo source, the caller and the error classifier are injected. Standard library only. The package
is named `meavatar`.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/own-avatar@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Implement `meavatar.AvatarSource` over your photo cache: `Handle(botID, userID)` makes the handle for a user (an empty
   handle means "no photo to name"), and `Photo(ctx, botID, handle)` returns the bytes and the content type.
2. `Photo` answers a user with no photo, or an unusable handle, with an error that has the method `NotFound() bool`
   returning true; any other error is a failed fetch (502).
3. Write a `meavatar.Caller` that reads the user your auth layer put on the request context and returns
   `(botID, userID, ok)`; `ok` false means no caller (401).
4. Mount it behind your auth: `mux.Handle("GET "+meavatar.Path, meavatar.Handler(src, caller, class))`.

## Configure

- `src`: the `AvatarSource`; nil answers 503, so a service with no photo cache still starts.
- `caller`: the `Caller` that names the signed-in user.
- `class`: a `func(error) string` that names an error's kind for the log line, never its text; nil names every error
  `"other"`.
- `Path` is the route path, `/api/miniapp/v1/me/avatar`; mount the handler elsewhere by using your own pattern.

## API

<!-- api: devkit foundation api; do not edit -->
```go
CONSTANTS
const Path = "/api/miniapp/v1/me/avatar"
FUNCTIONS
func Handler(src AvatarSource, caller Caller, class func(error) string) http.Handler
TYPES
type AvatarSource interface {
	Handle(botID string, userID int64) string
	Photo(ctx context.Context, botID, handle string) ([]byte, string, error)
}
type Caller func(ctx context.Context) (botID string, userID int64, ok bool)
```

## Example

```go
src := myPhotoCache{} // implements meavatar.AvatarSource
caller := func(ctx context.Context) (string, int64, bool) {
	u, ok := authUser(ctx) // set by your auth middleware
	return u.BotID, u.ID, ok
}
mux := http.NewServeMux()
mux.Handle("GET "+meavatar.Path, authMiddleware(meavatar.Handler(src, caller, nil)))
```

A request to that route answers the caller's own photo whatever its query names (`?user=2`, `?handle=h2` change nothing).

## Extension points

- `AvatarSource`: where photos come from and how a handle is made; the handler never builds a handle itself.
- `Caller`: how the signed-in user is read from the request context.
- `class`: how an error is named in the log line.

## Do not

- Do not add a handle, user id or any user selector to the route: the whole point is that the request cannot choose whose
  photo it gets.
- Do not put a user id, name or handle in a log line or a `class` result.
- Do not put a shared cache in front of the route: the response is `private, max-age=86400` for the caller's own client.
- Do not use it to serve other users' photos; write a separate handler with its own authorisation for that.
