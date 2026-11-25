package caldavclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// IsConflict reports whether the server refused a write because the object is
// not as the caller thought (412 Precondition Failed: the ETag no longer
// matches, or the object already exists on a create).
func IsConflict(err error) bool {
	var s *StatusError
	return errors.As(err, &s) && s.Code == http.StatusPreconditionFailed
}

// IsNotFound reports whether the server said the object does not exist.
func IsNotFound(err error) bool {
	var s *StatusError
	return errors.As(err, &s) && (s.Code == http.StatusNotFound || s.Code == http.StatusGone)
}

// IsWriteRefused reports whether the server refuses writes by this user
// (401/403), as on a calendar shared read-only.
func IsWriteRefused(err error) bool { return IsUnauthorized(err) }

// quoteETag makes an ETag usable in an If-Match header. The client hands out
// ETags without their quotes; a weak or already quoted tag is kept.
func quoteETag(etag string) string {
	if strings.HasPrefix(etag, `"`) || strings.HasPrefix(etag, "W/") {
		return etag
	}
	return `"` + etag + `"`
}

// Put stores raw iCalendar text at objectURL, byte for byte. With an empty
// etag it creates the object and fails with a conflict if it exists; with an
// etag it replaces that version and fails with a conflict if the server's
// version is another. It returns the new ETag as the server reports it, which
// is empty when the server leaves it out.
func (c *Client) Put(ctx context.Context, objectURL, raw, etag string) (string, error) {
	origin, _, err := c.origin(objectURL)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, abs(origin, c.route(objectURL)), strings.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	if etag == "" {
		req.Header.Set("If-None-Match", "*")
	} else {
		req.Header.Set("If-Match", quoteETag(etag))
	}
	res, err := c.auth.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return "", statusError(res)
	}
	if tag := res.Header.Get("ETag"); tag != "" {
		return unquoteETag(tag), nil
	}
	return "", nil
}

// Delete removes an object. With an etag it removes only that version (a
// conflict otherwise); an object that is already gone is a not-found error.
func (c *Client) Delete(ctx context.Context, objectURL, etag string) error {
	origin, _, err := c.origin(objectURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, abs(origin, c.route(objectURL)), nil)
	if err != nil {
		return err
	}
	if etag != "" {
		req.Header.Set("If-Match", quoteETag(etag))
	}
	res, err := c.auth.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return statusError(res)
	}
	return nil
}
