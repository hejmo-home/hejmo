package publiccert_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/libdns/libdns"
	"github.com/pquerna/otp/totp"
)

// The three new DNS providers run against fakes of their APIs (no real
// accounts): INWX's JSON-RPC, Cloudflare's REST API and Hetzner's Cloud DNS
// REST API. Each must set the box's address, add and delete the DNS-01
// challenge records (several values at one name) and list a zone.

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

const providerZone = "example.org."

func addr(name, ip string) libdns.Record {
	return libdns.Address{Name: name, IP: mustAddr(ip), TTL: 5 * time.Minute}
}

func txt(name, text string) libdns.Record {
	return libdns.TXT{Name: name, Text: text, TTL: 5 * time.Minute}
}

func records(t *testing.T, p publiccert.DNSProvider, name, typ string) []string {
	t.Helper()
	all, err := p.GetRecords(context.Background(), providerZone)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range all {
		if rr := r.RR(); rr.Name == name && rr.Type == typ {
			out = append(out, rr.Data)
		}
	}
	slices.Sort(out)
	return out
}

func sameStrings(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

// providerSuite is what every provider has to do for the box.
func providerSuite(t *testing.T, p publiccert.DNSProvider) {
	ctx := context.Background()
	zone := providerZone

	// Set: creates, then replaces (one record, not two).
	if _, err := p.SetRecords(ctx, zone, []libdns.Record{addr("box", "192.168.178.20")}); err != nil {
		t.Fatalf("set: %v", err)
	}
	sameStrings(t, "A after set", records(t, p, "box", "A"), "192.168.178.20")
	if _, err := p.SetRecords(ctx, zone, []libdns.Record{addr("box", "192.168.178.21")}); err != nil {
		t.Fatalf("set again: %v", err)
	}
	sameStrings(t, "A after second set", records(t, p, "box", "A"), "192.168.178.21")

	// GetRecords lists what is there, including other names (the name-taken check).
	if _, err := p.SetRecords(ctx, zone, []libdns.Record{addr("@", "203.0.113.7")}); err != nil {
		t.Fatalf("set apex: %v", err)
	}
	sameStrings(t, "apex A", records(t, p, "@", "A"), "203.0.113.7")

	// Two challenge values at one name (name and wildcard), then delete one.
	if _, err := p.AppendRecords(ctx, zone, []libdns.Record{txt("_acme-challenge.box", "token-one")}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := p.AppendRecords(ctx, zone, []libdns.Record{txt("_acme-challenge.box", "token-two")}); err != nil {
		t.Fatalf("append second: %v", err)
	}
	sameStrings(t, "TXT", records(t, p, "_acme-challenge.box", "TXT"), "token-one", "token-two")
	if _, err := p.DeleteRecords(ctx, zone, []libdns.Record{txt("_acme-challenge.box", "token-one")}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	sameStrings(t, "TXT after delete", records(t, p, "_acme-challenge.box", "TXT"), "token-two")
	// Stale cleanup: the leftover goes the same way, and the name is free.
	if _, err := p.DeleteRecords(ctx, zone, []libdns.Record{txt("_acme-challenge.box", "token-two")}); err != nil {
		t.Fatalf("delete stale: %v", err)
	}
	sameStrings(t, "TXT after cleanup", records(t, p, "_acme-challenge.box", "TXT"))
	// Other records are untouched.
	sameStrings(t, "A still there", records(t, p, "box", "A"), "192.168.178.21")
}

func TestHetznerProviderSetsDeletesAndListsThroughTheCloudDNSAPI(t *testing.T) {
	f := newFakeHetzner(t)
	p := publiccert.NewHetzner(publiccert.Credentials{Token: "hz-token"}, f.srv.URL+"/v1")
	providerSuite(t, p)
	if f.dnsConsole {
		t.Error("the deprecated DNS Console API was used")
	}
	if f.txtUnquoted {
		t.Error("a TXT value was sent without quotes")
	}
}

func TestHetznerProviderRefusesAWrongToken(t *testing.T) {
	f := newFakeHetzner(t)
	p := publiccert.NewHetzner(publiccert.Credentials{Token: "wrong"}, f.srv.URL+"/v1")
	if _, err := p.SetRecords(context.Background(), providerZone, []libdns.Record{addr("box", "192.168.178.20")}); err == nil {
		t.Fatal("a wrong token was accepted")
	}
}

func TestHetznerDefaultEndpointIsTheCloudAPI(t *testing.T) {
	if got := publiccert.HetznerBaseURL; got != "https://api.hetzner.cloud/v1" {
		t.Errorf("base URL = %q", got)
	}
}

func TestCloudflareProviderSetsDeletesAndListsAndUsesTheZoneToken(t *testing.T) {
	f := newFakeCloudflare(t)
	p := publiccert.NewCloudflare(publiccert.Credentials{Token: "cf-edit", ZoneToken: "cf-read"}, f.client())
	providerSuite(t, p)
	if f.zoneLookupAuth != "Bearer cf-read" {
		t.Errorf("the zone was looked up with %q, want the second token", f.zoneLookupAuth)
	}
	if f.editAuth != "Bearer cf-edit" {
		t.Errorf("records were changed with %q", f.editAuth)
	}
}

func TestCloudflareProviderWorksWithOneToken(t *testing.T) {
	f := newFakeCloudflare(t)
	p := publiccert.NewCloudflare(publiccert.Credentials{Token: "cf-edit"}, f.client())
	providerSuite(t, p)
	if f.zoneLookupAuth != "Bearer cf-edit" {
		t.Errorf("the zone was looked up with %q", f.zoneLookupAuth)
	}
}

func TestCloudflareProviderRefusesAWrongToken(t *testing.T) {
	f := newFakeCloudflare(t)
	p := publiccert.NewCloudflare(publiccert.Credentials{Token: "nope"}, f.client())
	if _, err := p.GetRecords(context.Background(), providerZone); err == nil {
		t.Fatal("a wrong token was accepted")
	}
}

func TestInwxProviderSetsDeletesAndListsAndLogsInWithTheSecret(t *testing.T) {
	f := newFakeINWX(t, "JBSWY3DPEHPK3PXP")
	p := publiccert.NewINWX(publiccert.Credentials{Username: "mueller-familie", Password: "pw", OTPSecret: "JBSWY3DPEHPK3PXP"}, f.srv.URL)
	providerSuite(t, p)
	if f.logins == 0 || f.logouts != f.logins {
		t.Errorf("logins %d, logouts %d: the session is not closed", f.logins, f.logouts)
	}
}

func TestInwxProviderWithoutTwoFactor(t *testing.T) {
	f := newFakeINWX(t, "")
	providerSuite(t, publiccert.NewINWX(publiccert.Credentials{Username: "mueller-familie", Password: "pw"}, f.srv.URL))
}

func TestInwxProviderRefusesAWrongPasswordOrSecret(t *testing.T) {
	f := newFakeINWX(t, "JBSWY3DPEHPK3PXP")
	for name, c := range map[string]publiccert.Credentials{
		"password": {Username: "mueller-familie", Password: "wrong", OTPSecret: "JBSWY3DPEHPK3PXP"},
		"secret":   {Username: "mueller-familie", Password: "pw", OTPSecret: "MFRGGZDFMZTWQ2LK"},
		"no 2FA":   {Username: "mueller-familie", Password: "pw"},
	} {
		p := publiccert.NewINWX(c, f.srv.URL)
		if _, err := p.GetRecords(context.Background(), providerZone); err == nil {
			t.Errorf("%s: login accepted", name)
		}
	}
}

// The factory builds the real providers for the names the wizard sends.
func TestDefaultProvidersKnowTheFourProvidersAndNoOthers(t *testing.T) {
	for _, name := range []string{"desec", "cloudflare", "hetzner", "inwx"} {
		if p, err := publiccert.DefaultProviders(name, publiccert.Credentials{Token: "x", Username: "u", Password: "p"}); err != nil || p == nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := publiccert.DefaultProviders("route53", publiccert.Credentials{Token: "x"}); err == nil {
		t.Error("an unknown provider was built")
	}
	if got := publiccert.Providers(); !slices.Equal(got, []string{"cloudflare", "desec", "hetzner", "inwx"}) {
		t.Errorf("Providers() = %v", got)
	}
}

// ---- fake INWX (JSON-RPC) ----

type inwxRec struct {
	ID, Name, Type, Content string
	TTL                     int
}

type fakeINWX struct {
	srv     *httptest.Server
	mu      sync.Mutex
	secret  string
	recs    []inwxRec
	next    int
	session map[string]bool // cookie -> unlocked
	logins  int
	logouts int
}

func newFakeINWX(t *testing.T, secret string) *fakeINWX {
	f := &fakeINWX{secret: secret, session: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeINWX) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req struct {
		Method string
		Params map[string]any
	}
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &req)
	str := func(k string) string { s, _ := req.Params[k].(string); return s }
	reply := func(code int, data any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": "x", "resData": data})
	}
	cookie, _ := r.Cookie("domrobot")
	sid := ""
	if cookie != nil {
		sid = cookie.Value
	}
	switch req.Method {
	case "account.login":
		if str("user") != "mueller-familie" || str("pass") != "pw" {
			reply(2200, nil)
			return
		}
		f.logins++
		sid = "s" + strconv.Itoa(f.logins)
		http.SetCookie(w, &http.Cookie{Name: "domrobot", Value: sid})
		f.session[sid] = f.secret == ""
		if f.secret != "" {
			reply(1000, map[string]string{"tfa": "GOOGLE-AUTH"})
			return
		}
		reply(1000, map[string]string{"tfa": "0"})
		return
	case "account.unlock":
		if sid == "" || !totp.Validate(str("tan"), f.secret) {
			reply(2200, nil)
			return
		}
		f.session[sid] = true
		reply(1000, nil)
		return
	case "account.logout":
		f.logouts++
		delete(f.session, sid)
		reply(1500, nil)
		return
	}
	if !f.session[sid] {
		reply(2200, nil)
		return
	}
	domain := strings.TrimSuffix(strings.ToLower(str("domain")), ".")
	if req.Method != "nameserver.deleteRecord" && req.Method != "nameserver.updateRecord" && domain != "example.org" {
		reply(2303, nil)
		return
	}
	full := func(n string) string {
		if n == "" || n == "@" || n == domain {
			return domain
		}
		if strings.HasSuffix(n, "."+domain) {
			return n
		}
		return n + "." + domain
	}
	switch req.Method {
	case "nameserver.info":
		var out []map[string]any
		for _, x := range f.recs {
			if (str("type") != "" && x.Type != str("type")) || (str("name") != "" && x.Name != full(str("name"))) || (str("content") != "" && x.Content != str("content")) {
				continue
			}
			out = append(out, map[string]any{"id": x.ID, "name": x.Name, "type": x.Type, "content": x.Content, "ttl": x.TTL, "prio": 0})
		}
		reply(1000, map[string]any{"domain": domain, "record": out})
	case "nameserver.createRecord":
		f.next++
		id := strconv.Itoa(f.next)
		ttl, _ := req.Params["ttl"].(float64)
		f.recs = append(f.recs, inwxRec{id, full(str("name")), str("type"), str("content"), int(ttl)})
		reply(1000, map[string]string{"id": id})
	case "nameserver.updateRecord":
		for i := range f.recs {
			if f.recs[i].ID == str("id") {
				f.recs[i].Content = str("content")
			}
		}
		reply(1000, nil)
	case "nameserver.deleteRecord":
		f.recs = slices.DeleteFunc(f.recs, func(x inwxRec) bool { return x.ID == str("id") })
		reply(1000, nil)
	default:
		reply(2000, nil)
	}
}

// ---- fake Cloudflare ----

type cfRec struct {
	ID, Name, Type, Content string
	TTL                     int
}

type fakeCloudflare struct {
	mu             sync.Mutex
	recs           []cfRec
	next           int
	zoneLookupAuth string
	editAuth       string
}

func newFakeCloudflare(t *testing.T) *fakeCloudflare { return &fakeCloudflare{} }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (f *fakeCloudflare) client() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		f.serve(rec, r)
		return rec.Result(), nil
	})}
}

func (f *fakeCloudflare) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	auth := r.Header.Get("Authorization")
	reply := func(status int, result any, total int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		out := map[string]any{"success": status < 400, "errors": []any{}, "result": result}
		if status >= 400 {
			out["errors"] = []map[string]any{{"code": 10000, "message": "Authentication error"}}
		}
		if total >= 0 {
			out["result_info"] = map[string]int{"page": 1, "per_page": 100, "count": total, "total_count": total}
		}
		_ = json.NewEncoder(w).Encode(out)
	}
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	if auth != "Bearer cf-edit" && auth != "Bearer cf-read" {
		reply(403, nil, -1)
		return
	}
	q := r.URL.Query()
	norm := func(n string) string {
		if n == "@" || n == "" || n == "example.org" {
			return "example.org"
		}
		return strings.TrimSuffix(n, ".example.org") + ".example.org"
	}
	switch {
	case path == "/zones" && r.Method == http.MethodGet:
		f.zoneLookupAuth = auth
		// Cloudflare takes the zone name with or without the final dot, as
		// certmagic and libdns pass it.
		if strings.TrimSuffix(q.Get("name"), ".") != "example.org" {
			reply(200, []any{}, 0)
			return
		}
		reply(200, []map[string]string{{"id": "zone1", "name": "example.org"}}, 1)
		return
	case strings.HasPrefix(path, "/zones/zone1/dns_records"):
	default:
		reply(404, nil, -1)
		return
	}
	if r.Method != http.MethodGet {
		f.editAuth = auth
		if auth != "Bearer cf-edit" {
			reply(403, nil, -1)
			return
		}
	}
	id := strings.TrimPrefix(strings.TrimPrefix(path, "/zones/zone1/dns_records"), "/")
	view := func(x cfRec) map[string]any {
		return map[string]any{"id": x.ID, "name": x.Name, "type": x.Type, "content": x.Content, "ttl": x.TTL}
	}
	var in struct {
		Name, Type, Content string
		TTL                 int
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	switch r.Method {
	case http.MethodGet:
		out := []map[string]any{}
		for _, x := range f.recs {
			if q.Get("type") != "" && x.Type != q.Get("type") {
				continue
			}
			if q.Get("name") != "" && x.Name != q.Get("name") {
				continue
			}
			if c := q.Get("content.exact"); c != "" && x.Content != c {
				continue
			}
			if c := q.Get("content.contains"); c != "" && !strings.Contains(x.Content, c) {
				continue
			}
			out = append(out, view(x))
		}
		reply(200, out, len(out))
	case http.MethodPost:
		f.next++
		x := cfRec{"r" + strconv.Itoa(f.next), norm(in.Name), in.Type, in.Content, in.TTL}
		f.recs = append(f.recs, x)
		reply(200, view(x), -1)
	case http.MethodPatch:
		for i := range f.recs {
			if f.recs[i].ID == id {
				f.recs[i].Content = in.Content
				reply(200, view(f.recs[i]), -1)
				return
			}
		}
		reply(404, nil, -1)
	case http.MethodDelete:
		for i, x := range f.recs {
			if x.ID == id {
				f.recs = slices.Delete(f.recs, i, i+1)
				reply(200, view(x), -1)
				return
			}
		}
		reply(404, nil, -1)
	}
}

// ---- fake Hetzner Cloud DNS ----

type fakeHetzner struct {
	srv         *httptest.Server
	mu          sync.Mutex
	rrsets      map[string]*hzSet // "name/TYPE"
	dnsConsole  bool
	txtUnquoted bool
}

type hzSet struct {
	Name    string
	Type    string
	TTL     int
	Records []string
}

func newFakeHetzner(t *testing.T) *fakeHetzner {
	f := &fakeHetzner{rrsets: map[string]*hzSet{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHetzner) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": code}})
	}
	if strings.Contains(r.URL.Host, "dns.hetzner.com") || strings.HasPrefix(r.URL.Path, "/api/v1") {
		f.dnsConsole = true
	}
	if r.Header.Get("Authorization") != "Bearer hz-token" {
		fail(http.StatusUnauthorized, "unauthorized")
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/v1/zones/example.org")
	if !ok {
		fail(http.StatusNotFound, "not_found")
		return
	}
	var in struct {
		Name    string
		Type    string
		TTL     int
		Records []struct{ Value string }
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	values := func() []string {
		var v []string
		for _, x := range in.Records {
			if in.Type == "TXT" || strings.Contains(r.URL.Path, "/TXT/") || strings.HasSuffix(r.URL.Path, "/TXT") {
				if !strings.HasPrefix(x.Value, `"`) {
					f.txtUnquoted = true
				}
			}
			v = append(v, x.Value)
		}
		return v
	}
	view := func(s *hzSet) map[string]any {
		var recs []map[string]string
		for _, v := range s.Records {
			recs = append(recs, map[string]string{"value": v})
		}
		return map[string]any{"id": s.Name + "/" + s.Type, "name": s.Name, "type": s.Type, "ttl": s.TTL, "records": recs}
	}
	action := map[string]any{"id": 1, "status": "success"}
	switch {
	case rest == "/rrsets" && r.Method == http.MethodGet:
		var all []map[string]any
		for _, s := range f.rrsets {
			all = append(all, view(s))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rrsets": all, "meta": map[string]any{"pagination": map[string]any{"page": 1, "per_page": 100, "next_page": nil, "last_page": 1, "total_entries": len(all)}}})
	case rest == "/rrsets" && r.Method == http.MethodPost:
		key := in.Name + "/" + in.Type
		if _, dup := f.rrsets[key]; dup {
			fail(http.StatusConflict, "uniqueness_error")
			return
		}
		f.rrsets[key] = &hzSet{in.Name, in.Type, in.TTL, values()}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"rrset": view(f.rrsets[key]), "action": action})
	default:
		parts := strings.Split(strings.TrimPrefix(rest, "/rrsets/"), "/")
		if len(parts) < 2 {
			fail(http.StatusNotFound, "not_found")
			return
		}
		name, _ := url.PathUnescape(parts[0])
		key := name + "/" + parts[1]
		s := f.rrsets[key]
		if s == nil {
			fail(http.StatusNotFound, "not_found")
			return
		}
		switch {
		case len(parts) == 2 && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"rrset": view(s)})
		case len(parts) == 2 && r.Method == http.MethodDelete:
			delete(f.rrsets, key)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"action": action})
		case len(parts) == 4 && r.Method == http.MethodPost:
			in.Type = parts[1]
			switch parts[3] {
			case "set_records":
				s.Records = values()
			case "add_records":
				s.Records = append(s.Records, values()...)
			case "remove_records":
				rm := values()
				s.Records = slices.DeleteFunc(s.Records, func(v string) bool { return slices.Contains(rm, v) })
				if len(s.Records) == 0 {
					fail(http.StatusUnprocessableEntity, "invalid_input") // an RRSet cannot be left empty
					return
				}
			case "change_ttl":
				s.TTL = in.TTL
			default:
				fail(http.StatusNotFound, "not_found")
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"action": action})
		default:
			fail(http.StatusNotFound, "not_found")
		}
	}
}

var _ = fmt.Sprintf
