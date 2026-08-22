package httpx

import "net/http"

// Middleware is used for wrapping an http.Handler or http.HandlerFunc
// to add cross-cutting behavior before or after the wrapped handler.
type Middleware interface {
	Wrap(next http.HandlerFunc) http.HandlerFunc
}
