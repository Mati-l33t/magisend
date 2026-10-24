package main

import (
	"net/http"
	"time"
)

// Bound a stalled peer, not the total duration of an actively streaming large file.
type idleResponseWriter struct{ http.ResponseWriter }

func (w *idleResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *idleResponseWriter) Write(b []byte) (int, error) {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	return w.ResponseWriter.Write(b)
}
