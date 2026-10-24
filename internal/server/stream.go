package server

import (
	"net/http"
	"time"
)

// Bound a stalled or crawling peer, not the total duration of an actively
// streaming large file: the write deadline only moves forward once the peer
// has taken at least minProgress bytes since it last moved, so a client has
// to keep up about 2 KB/s to hold on to its connection and download slot.
const (
	writeWindow = 2 * time.Minute
	minProgress = 256 << 10
)

type idleResponseWriter struct {
	http.ResponseWriter
	sent    int64
	started bool
}

func (w *idleResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *idleResponseWriter) Write(b []byte) (int, error) {
	if !w.started || w.sent >= minProgress {
		_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(writeWindow))
		w.started, w.sent = true, 0
	}
	n, e := w.ResponseWriter.Write(b)
	w.sent += int64(n)
	return n, e
}
