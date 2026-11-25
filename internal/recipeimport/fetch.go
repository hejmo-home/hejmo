package recipeimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html/charset"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/picture"
)

// Limits of reading a page (ticket 289).
const (
	// DefaultTimeout is the longest the box waits for a page, from the first byte sent to the last read.
	DefaultTimeout = 20 * time.Second
	// MaxBytes is the largest page Hejmo reads.
	MaxBytes     = 5 << 20
	maxRedirects = 5
)

// Errors of reading a page, which the pages turn into a sentence at the field.
var (
	ErrScheme           = errors.New("recipeimport: only http and https addresses are read")
	ErrAddress          = errors.New("recipeimport: this is not a usable web address")
	ErrTimeout          = errors.New("recipeimport: the site took too long")
	ErrTooManyRedirects = errors.New("recipeimport: too many redirects")
	ErrTooLarge         = errors.New("recipeimport: the page is larger than 5 MB")
	// ErrUnknownHost: the name does not exist.
	ErrUnknownHost = errors.New("recipeimport: no such site")
	// ErrUnreachable: nobody answers, or the certificate is broken.
	ErrUnreachable = errors.New("recipeimport: the site cannot be reached")
)

// PrivateError says the address, or a redirect or name behind it, leads to the own network or is a
// plain number address (Addr is that address, or the host as it was written).
type PrivateError struct{ Addr string }

func (e *PrivateError) Error() string {
	return "recipeimport: " + e.Addr + " is not a web site on the internet"
}

// StatusError is an answer other than 200.
type StatusError struct{ Code int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("recipeimport: the site answered %d", e.Code)
}

// NotHTMLError says the address holds a file (Type is its media type, "" when none was named).
type NotHTMLError struct{ Type string }

func (e *NotHTMLError) Error() string { return "recipeimport: this is not a web page but " + e.Type }

// Page is a page that was read.
type Page struct {
	Body []byte // as UTF-8
	URL  *url.URL
	Host string
}

// Fetcher reads one web page. Server-side request forgery is the threat: whoever types an address makes the
// box send a request, so it connects to public names only: no number addresses, nothing in the home network,
// also not after a redirect or a name that changes its answer. The check sits where the connection is made.
type Fetcher struct {
	// Gate is the box's door to the outside (ADR 0024); the page counts as outbound.Recipes.
	Gate *outbound.Gate
	// Public says whether the box may connect to an address; nil uses picture.IsPublic.
	Public func(netip.Addr) bool
	// AllowIPLiteral lets an address written as numbers through (tests only, the test server is one).
	AllowIPLiteral bool
	// Timeout is the longest the read may take; zero means DefaultTimeout.
	Timeout time.Duration
}

func (f *Fetcher) public(a netip.Addr) bool {
	if f.Public != nil {
		return f.Public(a)
	}
	return picture.IsPublic(a)
}

func (f *Fetcher) literal(host string) error {
	h := strings.Trim(strings.ToLower(host), "[].")
	if a, err := netip.ParseAddr(h); err == nil && !f.AllowIPLiteral {
		return &PrivateError{Addr: a.String()}
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return &PrivateError{Addr: host}
	}
	return nil
}

// Fetch reads the page at rawURL.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Page, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Page{}, ErrAddress
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return Page{}, ErrScheme
	}
	if u.Hostname() == "" || u.User != nil {
		return Page{}, ErrAddress
	}
	if err := f.literal(u.Hostname()); err != nil {
		return Page{}, err
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := f.Gate.NewClient(outbound.Recipes, outbound.ClientConfig{
		Timeout: timeout,
		AllowAddr: func(a netip.Addr) error {
			if !f.public(a) {
				return &PrivateError{Addr: a.String()}
			}
			return nil
		},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		NoKeepAlive:           true,
		MaxHeaderBytes:        64 << 10,
		RecordPath:            false,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return ErrTooManyRedirects
			}
			return f.literal(req.URL.Hostname())
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Page{}, ErrAddress
	}
	req.Header.Set("User-Agent", "Hejmo (recipe import by a family box)")
	req.Header.Set("Accept", "text/html, application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		return Page{}, Classify(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Page{}, &StatusError{Code: resp.StatusCode}
	}
	ct := resp.Header.Get("Content-Type")
	if mt, _, _ := mime.ParseMediaType(ct); mt != "" && mt != "text/html" && mt != "application/xhtml+xml" {
		return Page{}, &NotHTMLError{Type: mt}
	}
	if resp.ContentLength > MaxBytes {
		return Page{}, ErrTooLarge
	}
	r, err := charset.NewReader(io.LimitReader(resp.Body, MaxBytes+1), ct)
	if err != nil {
		return Page{}, Classify(ctx, err)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return Page{}, Classify(ctx, err)
	}
	if len(body) > MaxBytes+MaxBytes/4 || (resp.ContentLength < 0 && len(body) > MaxBytes) {
		return Page{}, ErrTooLarge
	}
	final := resp.Request.URL
	return Page{Body: body, URL: final, Host: strings.ToLower(final.Hostname())}, nil
}

// Classify turns what the HTTP client returned into one of this package's errors.
func Classify(ctx context.Context, err error) error {
	var private *PrivateError
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(err, &private):
		return private
	case errors.Is(err, outbound.ErrRefused):
		return err // offline mode or switched off: the caller says so in words
	case errors.Is(err, ErrTooManyRedirects):
		return ErrTooManyRedirects
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return ErrTimeout
	case errors.As(err, &dns) && dns.IsNotFound:
		return ErrUnknownHost
	case errors.As(err, &ne) && ne.Timeout():
		return ErrTimeout
	}
	return fmt.Errorf("%w: %v", ErrUnreachable, err)
}
