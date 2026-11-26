package iosprofile_test

import (
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/iosprofile"
)

// node is a decoded property list value: a dict, an array or a scalar.
type node struct {
	kind  string // dict, array, string, integer, true, false, data
	text  string
	dict  map[string]*node
	items []*node
}

func parsePlist(t *testing.T, raw []byte) *node {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	dec.Strict = true
	dec.Entity = nil
	var value func(start xml.StartElement) *node
	value = func(start xml.StartElement) *node {
		n := &node{kind: start.Name.Local}
		switch n.kind {
		case "dict":
			n.dict = map[string]*node{}
			var key string
			for {
				tok, err := dec.Token()
				if err != nil {
					t.Fatalf("plist: %v", err)
				}
				switch e := tok.(type) {
				case xml.StartElement:
					if e.Name.Local == "key" {
						var k string
						if err := dec.DecodeElement(&k, &e); err != nil {
							t.Fatal(err)
						}
						key = k
					} else {
						n.dict[key] = value(e)
					}
				case xml.EndElement:
					return n
				}
			}
		case "array":
			for {
				tok, err := dec.Token()
				if err != nil {
					t.Fatalf("plist: %v", err)
				}
				switch e := tok.(type) {
				case xml.StartElement:
					n.items = append(n.items, value(e))
				case xml.EndElement:
					return n
				}
			}
		case "true", "false":
			_ = dec.Skip()
		default:
			var s string
			if err := dec.DecodeElement(&s, &start); err != nil {
				t.Fatal(err)
			}
			n.text = s
		}
		return n
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			t.Fatal("no plist element")
		}
		if err != nil {
			t.Fatalf("plist: %v", err)
		}
		if e, ok := tok.(xml.StartElement); ok && e.Name.Local == "plist" {
			for {
				tok, _ := dec.Token()
				if e, ok := tok.(xml.StartElement); ok {
					return value(e)
				}
			}
		}
	}
}

var params = iosprofile.Params{
	DeviceName: "iPhone Mama", Host: "box.example.org", Port: 443, Username: "mama",
	Token: "hejmo_secret", PrincipalPath: "/dav/0b1e8a58-0c6e-4a63-9b3e-0d6f6a1c2d3e/",
	Identifier: "org.hejmo.box.caldav.0b1e8a58", ProfileUUID: "11111111-2222-4333-8444-555555555555", PayloadUUID: "66666666-7777-4888-9999-000000000000",
}

// Apple's documented keys for com.apple.caldav.account (Configuration Profile
// Reference): the payload must only use these, with these types.
var caldavKeys = map[string]string{
	"CalDAVAccountDescription": "string", "CalDAVHostName": "string", "CalDAVPort": "integer",
	"CalDAVPrincipalURL": "string", "CalDAVUsername": "string", "CalDAVPassword": "string", "CalDAVUseSSL": "bool",
}
var commonKeys = map[string]string{
	"PayloadType": "string", "PayloadVersion": "integer", "PayloadIdentifier": "string",
	"PayloadUUID": "string", "PayloadDisplayName": "string", "PayloadDescription": "string", "PayloadOrganization": "string",
}

func checkTypes(t *testing.T, d *node, allowed ...map[string]string) {
	t.Helper()
	for k, v := range d.dict {
		want := ""
		for _, set := range allowed {
			if w, ok := set[k]; ok {
				want = w
			}
		}
		got := v.kind
		if got == "true" || got == "false" {
			got = "bool"
		}
		if want == "" || got != want {
			t.Errorf("key %q of kind %q is not an Apple key of that type", k, v.kind)
		}
	}
}

func TestProfileIsAConfigurationProfileWithOneCalDAVAccount(t *testing.T) {
	raw, err := iosprofile.CalDAV(params)
	if err != nil {
		t.Fatal(err)
	}
	root := parsePlist(t, raw)
	checkTypes(t, root, commonKeys, map[string]string{"PayloadContent": "array", "PayloadRemovalDisallowed": "bool"})
	if root.dict["PayloadType"].text != "Configuration" || root.dict["PayloadVersion"].text != "1" ||
		root.dict["PayloadUUID"].text != params.ProfileUUID || root.dict["PayloadIdentifier"].text != params.Identifier {
		t.Fatalf("profile envelope: %+v", root.dict)
	}
	content := root.dict["PayloadContent"]
	if content == nil || len(content.items) != 1 {
		t.Fatalf("want exactly one payload, got %+v", content)
	}
	acc := content.items[0]
	checkTypes(t, acc, commonKeys, caldavKeys)
	text := func(k string) string {
		if n := acc.dict[k]; n != nil {
			return n.text
		}
		return ""
	}
	for key, want := range map[string]string{
		"PayloadType": "com.apple.caldav.account", "PayloadVersion": "1", "PayloadUUID": params.PayloadUUID,
		"CalDAVHostName": "box.example.org", "CalDAVPort": "443", "CalDAVUsername": "mama", "CalDAVPassword": "hejmo_secret",
		"CalDAVPrincipalURL": "https://box.example.org" + params.PrincipalPath, "CalDAVAccountDescription": "iPhone Mama",
	} {
		if got := text(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if acc.dict["CalDAVUseSSL"] == nil || acc.dict["CalDAVUseSSL"].kind != "true" {
		t.Error("CalDAVUseSSL must be true")
	}
	for _, key := range []string{"PayloadIdentifier", "PayloadDisplayName"} {
		if text(key) == "" {
			t.Errorf("%s is required by Apple and missing", key)
		}
	}
}

func TestProfileEscapesWhatTheParentTypes(t *testing.T) {
	p := params
	p.DeviceName = `Mama's <iPhone> & "more"`
	raw, err := iosprofile.CalDAV(p)
	if err != nil {
		t.Fatal(err)
	}
	acc := parsePlist(t, raw).dict["PayloadContent"].items[0]
	if acc.dict["CalDAVAccountDescription"].text != p.DeviceName {
		t.Fatalf("description = %q", acc.dict["CalDAVAccountDescription"].text)
	}
}

func TestProfileNeedsItsParts(t *testing.T) {
	for _, mod := range []func(*iosprofile.Params){
		func(p *iosprofile.Params) { p.Host = "" }, func(p *iosprofile.Params) { p.Token = "" },
		func(p *iosprofile.Params) { p.Username = "" }, func(p *iosprofile.Params) { p.Port = 0 },
	} {
		p := params
		mod(&p)
		if _, err := iosprofile.CalDAV(p); err == nil {
			t.Errorf("%+v: want an error", p)
		}
	}
}

func TestNewUUIDIsAVersion4UUIDAndDiffers(t *testing.T) {
	re := regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-4[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`)
	a, b := iosprofile.NewUUID(), iosprofile.NewUUID()
	if !re.MatchString(a) || a == b {
		t.Fatalf("uuids %q %q", a, b)
	}
}

// Apple's documented keys for com.apple.carddav.account.
var carddavKeys = map[string]string{
	"CardDAVAccountDescription": "string", "CardDAVHostName": "string", "CardDAVPort": "integer",
	"CardDAVPrincipalURL": "string", "CardDAVUsername": "string", "CardDAVPassword": "string", "CardDAVUseSSL": "bool",
}

// Ticket 299: the same profile sets up the address book.
func TestProfileHoldsACardDAVAccountWhenAskedTo(t *testing.T) {
	p := params
	p.CardDAVPrincipalPath = "/carddav/0b1e8a58-0c6e-4a63-9b3e-0d6f6a1c2d3e/"
	p.CardDAVPayloadUUID = "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"
	raw, err := iosprofile.CalDAV(p)
	if err != nil {
		t.Fatal(err)
	}
	content := parsePlist(t, raw).dict["PayloadContent"]
	if len(content.items) != 2 {
		t.Fatalf("want a CalDAV and a CardDAV payload, got %d", len(content.items))
	}
	acc := content.items[1]
	checkTypes(t, acc, commonKeys, carddavKeys)
	for key, want := range map[string]string{
		"PayloadType": "com.apple.carddav.account", "PayloadUUID": p.CardDAVPayloadUUID, "PayloadIdentifier": p.Identifier + ".contacts",
		"CardDAVHostName": "box.example.org", "CardDAVPort": "443", "CardDAVUsername": "mama", "CardDAVPassword": "hejmo_secret",
		"CardDAVPrincipalURL": "https://box.example.org" + p.CardDAVPrincipalPath, "CardDAVAccountDescription": "iPhone Mama",
	} {
		if got := acc.dict[key]; got == nil || got.text != want {
			t.Errorf("%s = %+v, want %q", key, got, want)
		}
	}
	if acc.dict["CardDAVUseSSL"] == nil || acc.dict["CardDAVUseSSL"].kind != "true" {
		t.Error("CardDAVUseSSL must be true")
	}
	if content.items[0].dict["PayloadUUID"].text == acc.dict["PayloadUUID"].text {
		t.Error("two payloads, one UUID")
	}
	p.CardDAVPayloadUUID = ""
	if _, err := iosprofile.CalDAV(p); err == nil {
		t.Error("a CardDAV payload needs its UUID")
	}
}
