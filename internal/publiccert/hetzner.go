package publiccert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

// hetznerBaseURL is the Hetzner Cloud API. Only its DNS part is used, never
// the deprecated DNS Console API (decision Q30).
const hetznerBaseURL = "https://api.hetzner.cloud/v1"

// hetzner is Hejmo's own thin libdns provider over the Hetzner Cloud DNS REST
// API (net/http only): the libdns module would add 4.6 MB and some 45 modules
// to the binary. It keeps what the box needs: set, append, delete and list.
type hetzner struct {
	token, base string
	client      *http.Client
}

func newHetzner(c Credentials, base string) *hetzner {
	return &hetzner{token: c.Token, base: strings.TrimSuffix(base, "/"), client: &http.Client{Timeout: 30 * time.Second}}
}

type hetznerRecord struct {
	Value string `json:"value"`
}

type hetznerRRSet struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	TTL     *int            `json:"ttl"`
	Records []hetznerRecord `json:"records"`
}

// errHetznerNotFound is a 404 of the API.
var errHetznerNotFound = errors.New("hetzner: not found")

// do sends a request and decodes a JSON answer into out (when not nil). An
// action that ended in an error is an error too.
func (h *hetzner) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusNotFound {
		return errHetznerNotFound
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("hetzner: %s %s: HTTP %d %s", method, path, res.StatusCode, e.Error.Code)
	}
	var act struct {
		Action *struct {
			Status string `json:"status"`
			Error  struct{ Code, Message string }
		} `json:"action"`
	}
	if json.Unmarshal(raw, &act) == nil && act.Action != nil && act.Action.Status == "error" {
		return fmt.Errorf("hetzner: %s %s: %s", method, path, act.Action.Error.Code)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func zonePath(zone string) string { return "/zones/" + url.PathEscape(strings.TrimSuffix(zone, ".")) }

func rrsetPath(zone, name, typ string) string {
	return zonePath(zone) + "/rrsets/" + url.PathEscape(name) + "/" + url.PathEscape(typ)
}

// quoteTXT writes a TXT value the way the zone file format wants it.
func quoteTXT(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

func unquoteTXT(v string) string {
	if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		v = strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(v[1 : len(v)-1])
	}
	return v
}

func wireValue(rr libdns.RR) string {
	if rr.Type == "TXT" {
		return quoteTXT(rr.Data)
	}
	return rr.Data
}

type setKey struct{ name, typ string }

// group collects records by name and type: Hetzner changes whole record sets.
func group(recs []libdns.Record) (keys []setKey, byKey map[setKey][]libdns.RR) {
	byKey = map[setKey][]libdns.RR{}
	for _, r := range recs {
		rr := r.RR()
		k := setKey{rr.Name, rr.Type}
		if _, ok := byKey[k]; !ok {
			keys = append(keys, k)
		}
		byKey[k] = append(byKey[k], rr)
	}
	return keys, byKey
}

func (h *hetzner) get(ctx context.Context, zone string, k setKey) (*hetznerRRSet, error) {
	var out struct {
		RRSet hetznerRRSet `json:"rrset"`
	}
	if err := h.do(ctx, http.MethodGet, rrsetPath(zone, k.name, k.typ), nil, &out); err != nil {
		if errors.Is(err, errHetznerNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out.RRSet, nil
}

func values(rrs []libdns.RR) []hetznerRecord {
	var out []hetznerRecord
	for _, rr := range rrs {
		out = append(out, hetznerRecord{wireValue(rr)})
	}
	return out
}

func ttlOf(rrs []libdns.RR) int {
	if rrs[0].TTL > 0 {
		return int(rrs[0].TTL.Seconds())
	}
	return 300
}

func (h *hetzner) create(ctx context.Context, zone string, k setKey, rrs []libdns.RR) error {
	ttl := ttlOf(rrs)
	return h.do(ctx, http.MethodPost, zonePath(zone)+"/rrsets", hetznerRRSet{Name: k.name, Type: k.typ, TTL: &ttl, Records: values(rrs)}, nil)
}

// SetRecords makes each name and type hold exactly the given values.
func (h *hetzner) SetRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	keys, by := group(recs)
	for _, k := range keys {
		cur, err := h.get(ctx, zone, k)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			if err := h.create(ctx, zone, k, by[k]); err != nil {
				return nil, err
			}
			continue
		}
		if err := h.do(ctx, http.MethodPost, rrsetPath(zone, k.name, k.typ)+"/actions/set_records", map[string]any{"records": values(by[k])}, nil); err != nil {
			return nil, err
		}
		if want := ttlOf(by[k]); cur.TTL == nil || *cur.TTL != want {
			if err := h.do(ctx, http.MethodPost, rrsetPath(zone, k.name, k.typ)+"/actions/change_ttl", map[string]any{"ttl": want}, nil); err != nil {
				return nil, err
			}
		}
	}
	return recs, nil
}

// AppendRecords adds values next to those already there.
func (h *hetzner) AppendRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	keys, by := group(recs)
	for _, k := range keys {
		cur, err := h.get(ctx, zone, k)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			if err := h.create(ctx, zone, k, by[k]); err != nil {
				return nil, err
			}
			continue
		}
		have := map[string]bool{}
		for _, r := range cur.Records {
			have[r.Value] = true
		}
		var add []libdns.RR
		for _, rr := range by[k] {
			if !have[wireValue(rr)] {
				add = append(add, rr)
			}
		}
		if len(add) == 0 {
			continue
		}
		if err := h.do(ctx, http.MethodPost, rrsetPath(zone, k.name, k.typ)+"/actions/add_records", map[string]any{"records": values(add)}, nil); err != nil {
			return nil, err
		}
	}
	return recs, nil
}

// DeleteRecords removes the given values; a record without data removes the
// whole set. A set left empty is deleted (the API does not keep empty sets).
func (h *hetzner) DeleteRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	keys, by := group(recs)
	for _, k := range keys {
		cur, err := h.get(ctx, zone, k)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			continue
		}
		drop := map[string]bool{}
		all := false
		for _, rr := range by[k] {
			if rr.Data == "" {
				all = true
			}
			drop[wireValue(rr)] = true
		}
		var rm []hetznerRecord
		for _, r := range cur.Records {
			if all || drop[r.Value] {
				rm = append(rm, r)
			}
		}
		switch {
		case len(rm) == 0:
		case len(rm) == len(cur.Records):
			if err := h.do(ctx, http.MethodDelete, rrsetPath(zone, k.name, k.typ), nil, nil); err != nil {
				return nil, err
			}
		default:
			if err := h.do(ctx, http.MethodPost, rrsetPath(zone, k.name, k.typ)+"/actions/remove_records", map[string]any{"records": rm}, nil); err != nil {
				return nil, err
			}
		}
	}
	return recs, nil
}

// GetRecords lists all records of the zone.
func (h *hetzner) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	var out []libdns.Record
	for page := 1; ; page++ {
		var res struct {
			RRSets []hetznerRRSet `json:"rrsets"`
			Meta   struct {
				Pagination struct {
					NextPage *int `json:"next_page"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		if err := h.do(ctx, http.MethodGet, fmt.Sprintf("%s/rrsets?per_page=100&page=%d", zonePath(zone), page), nil, &res); err != nil {
			return nil, err
		}
		for _, s := range res.RRSets {
			for _, r := range s.Records {
				rr := libdns.RR{Name: s.Name, Type: s.Type, Data: r.Value}
				if s.TTL != nil {
					rr.TTL = time.Duration(*s.TTL) * time.Second
				}
				if rr.Type == "TXT" {
					rr.Data = unquoteTXT(rr.Data)
				}
				rec, err := rr.Parse()
				if err != nil {
					rec = rr // a type libdns cannot parse is still listed
				}
				out = append(out, rec)
			}
		}
		if res.Meta.Pagination.NextPage == nil || page > 1000 {
			return out, nil
		}
	}
}

var (
	_ libdns.RecordGetter  = (*hetzner)(nil)
	_ libdns.RecordSetter  = (*hetzner)(nil)
	_ libdns.RecordDeleter = (*hetzner)(nil)
)
