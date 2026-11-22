// Package meavatar is an HTTP handler that serves the signed-in user's own photo, for a profile head or a bottom menu. It
// takes no handle and no user id from the request: the handle is made here from the user the caller names, so no other
// user's photo is reachable through it. The photo source, the caller and the error classifier are injected; the package
// imports only the standard library.
package meavatar

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

// Path is the route's path; the composition root mounts Handler on "GET " + Path behind the Mini App's auth.
const Path = "/api/miniapp/v1/me/avatar"

// cacheOwn is the Cache-Control of a photo: the caller's own picture, kept by their client for a day and by no shared cache.
const cacheOwn = "private, max-age=86400"

// AvatarSource is the part of the avatar cache this route uses (*avatars.Service implements it): a handle for a user of a
// bot, and the photo a handle stands for. Photo answers a user with no photo, or an unusable handle, with an error that has
// the method NotFound() bool returning true; any other error is a failed fetch.
type AvatarSource interface {
	Handle(botID string, userID int64) string
	Photo(ctx context.Context, botID, handle string) ([]byte, string, error)
}

// Caller names the signed-in user of a request: the bot that signed the initData and the user id; ok is false with none.
type Caller func(ctx context.Context) (botID string, userID int64, ok bool)

// fallbackClass is the error class of a log line when Handler is given no classifier.
const fallbackClass = "other"

// Handler answers the caller's own photo: 200 with the image and cacheOwn, 404 when they have no photo (or the source can
// make no handle for them), 401 with no caller, 503 with no source, 502 when the fetch fails. Its log lines hold the bot id,
// the status and the error class: no user id, name or handle. class names an error's kind for those lines, never its text;
// nil names every error "other".
func Handler(src AvatarSource, caller Caller, class func(error) string) http.Handler {
	if class == nil {
		class = func(error) string { return fallbackClass }
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		botID, userID, ok := caller(r.Context())
		if !ok {
			fail(w, http.StatusUnauthorized)
			return
		}
		if src == nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		handle := src.Handle(botID, userID)
		if handle == "" {
			fail(w, http.StatusNotFound)
			return
		}
		data, typ, err := src.Photo(r.Context(), botID, handle)
		switch {
		case notFound(err):
			fail(w, http.StatusNotFound)
			return
		case err != nil:
			slog.Warn("meavatar: photo failed", "bot_id", botID, "status", http.StatusBadGateway, "class", class(err))
			fail(w, http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", typ)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", cacheOwn)
		if _, err := w.Write(data); err != nil {
			slog.Warn("meavatar: write failed", "bot_id", botID, "class", class(err))
		}
	})
}

// fail writes an empty answer of status that no cache keeps: a photo added later shows on the next open.
func fail(w http.ResponseWriter, status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
}

// notFound says whether err is what an AvatarSource answers when there is nothing to show.
func notFound(err error) bool {
	var nf interface{ NotFound() bool }
	return errors.As(err, &nf) && nf.NotFound()
}
