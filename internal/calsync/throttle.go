package calsync

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/graph"
	"github.com/hejmo-home/hejmo/internal/i18n"
)

// MaxRetryAfter is the longest wait a server can ask for; more is read as this.
const MaxRetryAfter = 6 * time.Hour

// RetryAfter reads a Retry-After header, seconds or an HTTP date. A missing,
// unreadable, negative or past value is 0 (the caller uses its default wait);
// an absurd one is MaxRetryAfter.
func RetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var wait time.Duration
	if secs, err := strconv.ParseInt(value, 10, 64); err == nil {
		if secs > int64(MaxRetryAfter/time.Second) {
			return MaxRetryAfter
		}
		wait = time.Duration(secs) * time.Second
	} else if at, err := http.ParseTime(value); err == nil {
		wait = at.Sub(now)
	}
	return max(0, min(wait, MaxRetryAfter))
}

// throttledError says the server asked Hejmo to wait (429, or 503 for an
// overloaded server). Nothing more goes to it in this run.
type throttledError struct {
	wait  time.Duration // 0: the server named no time
	cause error
}

func (e *throttledError) Error() string { return "the server asked to wait: " + e.cause.Error() }
func (e *throttledError) Unwrap() error { return e.cause }

// throttleOf returns the throttling in err, wrapped so the callers stop.
func throttleOf(err error, now time.Time) *throttledError {
	var t *throttledError
	if errors.As(err, &t) {
		return t
	}
	var dav *caldavclient.StatusError
	if errors.As(err, &dav) && isThrottle(dav.Code) {
		return &throttledError{wait: RetryAfter(dav.RetryAfter, now), cause: err}
	}
	var gr *graph.StatusError
	if errors.As(err, &gr) && isThrottle(gr.Code) {
		return &throttledError{wait: RetryAfter(gr.RetryAfter, now), cause: err}
	}
	return nil
}

func isThrottle(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable
}

// failed says what the sync log says about a failure and when the next attempt
// is. A throttle is one "server asked to wait" entry, and the next attempt is
// not before the time the server named.
func failed(msg i18n.Message, cause error, at time.Time, interval time.Duration, failures int) (i18n.Message, time.Time) {
	wait := Backoff(interval, failures+1)
	if t := throttleOf(cause, at); t != nil {
		return i18n.Msg("sync.fail.throttled"), at.Add(max(wait, t.wait))
	}
	return msg, at.Add(wait)
}
