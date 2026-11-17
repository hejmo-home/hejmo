package picture

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// DefaultTimeout is the longest a download may take, from the first byte sent
// to the last byte read (the design says "höchstens 10 Sekunden").
const DefaultTimeout = 10 * time.Second

const maxRedirects = 5

// Errors of a download, beside those of Process, that the pages turn into a
// sentence at the field.
var (
	// ErrScheme: the address is not an http or https address.
	ErrScheme = errors.New("picture: only http and https addresses are taken")
	// ErrAddress: the address is not one the box can ask (no host, a user name or password in it).
	ErrAddress = errors.New("picture: this is not a usable web address")
	// ErrTimeout: the site took too long.
	ErrTimeout = errors.New("picture: the site took too long")
	// ErrTooManyRedirects: the address sends the box in circles.
	ErrTooManyRedirects = errors.New("picture: too many redirects")
	// ErrUnreachable: no connection (unknown name, nobody answers, broken certificate).
	ErrUnreachable = errors.New("picture: the site cannot be reached")
)

// PrivateError says the address, or a redirect or name behind it, leads to the
// own network and not to the internet.
type PrivateError struct{ Addr string }

func (e *PrivateError) Error() string {
	return "picture: " + e.Addr + " is not a public internet address"
}

// StatusError is an answer other than 200 OK.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("picture: the site answered %d", e.Code) }

// Fetched is a downloaded picture and the host it came from.
type Fetched struct {
	Result
	Host string
}

// Fetcher downloads a picture from a web address, once. Threat model, server-
// side request forgery: whoever types an address makes the box send a request,
// so the box only connects to public addresses. The check sits where the
// connection is made, on the address that is really used, so it holds for
// spellings of an address, names that point inside, redirects and a name that
// changes its answer between the check and the connection.
type Fetcher struct {
	// Public says whether the box may connect to an address; the zero Fetcher uses IsPublic.
	Public func(netip.Addr) bool
	// Timeout is the longest the download may take; zero means DefaultTimeout.
	Timeout time.Duration
}

func (f *Fetcher) public(a netip.Addr) bool {
	if f.Public != nil {
		return f.Public(a)
	}
	return IsPublic(a)
}

// Fetch downloads and processes the picture at rawURL.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Fetched, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		if strings.Contains(err.Error(), "first path segment in URL cannot contain colon") {
			return Fetched{}, ErrScheme
		}
		return Fetched{}, ErrAddress
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return Fetched{}, ErrScheme
	}
	if u.Hostname() == "" || u.User != nil {
		return Fetched{}, ErrAddress
	}

	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return &PrivateError{Addr: address}
			}
			if a := ap.Addr().Unmap(); !f.public(a) {
				return &PrivateError{Addr: a.String()}
			}
			return nil
		},
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                  nil, // an environment proxy would bypass the check
			DialContext:            dialer.DialContext,
			TLSHandshakeTimeout:    timeout,
			ResponseHeaderTimeout:  timeout,
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: 64 << 10,
		},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return ErrTooManyRedirects
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Fetched{}, ErrAddress
	}
	req.Header.Set("User-Agent", "Hejmo (picture download by a family box)")
	req.Header.Set("Accept", "image/jpeg, image/png, image/webp, image/gif")
	resp, err := client.Do(req)
	if err != nil {
		return Fetched{}, fetchError(ctx, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Fetched{}, &StatusError{Code: resp.StatusCode}
	}
	if resp.ContentLength > MaxBytes {
		return Fetched{}, &TooLargeError{Bytes: resp.ContentLength}
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt == "text/html" || mt == "application/xhtml+xml" {
		return Fetched{}, ErrWebPage
	}
	res, err := ProcessContext(ctx, resp.Body)
	if err != nil {
		var te *TooLargeError
		if errors.As(err, &te) || errors.Is(err, ErrNotAnImage) || errors.Is(err, ErrWebPage) || errors.Is(err, ErrTooManyPixels) {
			return Fetched{}, err
		}
		return Fetched{}, fetchError(ctx, err) // the body broke off
	}
	return Fetched{Result: res, Host: strings.ToLower(u.Hostname())}, nil
}

func fetchError(ctx context.Context, err error) error {
	var private *PrivateError
	switch {
	case errors.As(err, &private):
		return private
	case errors.Is(err, ErrTooManyRedirects):
		return ErrTooManyRedirects
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return ErrTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrTimeout
	}
	return fmt.Errorf("%w: %v", ErrUnreachable, err)
}

// Prefixes that are not on the public internet although they are global
// unicast addresses.
var privatePrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
		"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/96", "::1/128", "64:ff9b:1::/48", "fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8", "2001:db8::/32", "100::/64",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// IsPublic reports whether an address is on the public internet. Addresses
// that carry an IPv4 address inside (IPv4-mapped, 6to4, NAT64, Teredo) are as
// public as that IPv4 address.
func IsPublic(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	if !a.IsValid() {
		return false
	}
	if a.Is6() {
		b := a.As16()
		switch {
		case b[0] == 0x20 && b[1] == 0x02: // 6to4: the IPv4 address follows the prefix
			return IsPublic(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		case b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b && allZero(b[4:12]): // NAT64: 64:ff9b::/96
			return IsPublic(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		case b[0] == 0x20 && b[1] == 0x01 && b[2] == 0 && b[3] == 0: // Teredo: 2001::/32
			return false
		}
	}
	if !a.IsGlobalUnicast() {
		return false
	}
	for _, p := range privatePrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}
