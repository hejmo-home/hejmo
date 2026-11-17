package publiccert

import (
	"net/http"
	"slices"
	"strings"

	"github.com/libdns/cloudflare"
	"github.com/libdns/desec"
	"github.com/libdns/inwx"
)

// Credentials are what a DNS provider needs to be asked for changes. Which
// fields are used depends on the provider: Token for deSEC, Hetzner and
// Cloudflare (ZoneToken is Cloudflare's optional second token that only reads
// zones), Username, Password and OTPSecret for INWX. They are sealed together.
type Credentials struct {
	Token, ZoneToken              string
	Username, Password, OTPSecret string
}

// ProviderFactory builds the DNS provider client for credentials.
type ProviderFactory func(provider string, c Credentials) (DNSProvider, error)

// Providers names the DNS providers the box can use, sorted.
func Providers() []string {
	n := make([]string, 0, len(providerBuilders))
	for name := range providerBuilders {
		n = append(n, name)
	}
	slices.Sort(n)
	return n
}

var providerBuilders = map[string]func(Credentials) DNSProvider{
	"desec":      func(c Credentials) DNSProvider { return &desec.Provider{Token: c.Token} },
	"cloudflare": func(c Credentials) DNSProvider { return newCloudflare(c, nil) },
	"hetzner":    func(c Credentials) DNSProvider { return newHetzner(c, hetznerBaseURL) },
	"inwx":       func(c Credentials) DNSProvider { return newINWX(c, "") },
}

func defaultProviders(provider string, c Credentials) (DNSProvider, error) {
	if build, ok := providerBuilders[provider]; ok {
		return build(c), nil
	}
	return nil, ErrUnknownProvider
}

func newCloudflare(c Credentials, client *http.Client) DNSProvider {
	p := &cloudflare.Provider{APIToken: c.Token, ZoneToken: c.ZoneToken}
	if client != nil {
		p.HTTPClient = client
	}
	return p
}

func newINWX(c Credentials, endpoint string) DNSProvider {
	return &inwx.Provider{Username: c.Username, Password: c.Password, SharedSecret: c.OTPSecret, EndpointURL: endpoint}
}

// cleanSecret removes spaces and upper-cases a shared secret as INWX shows it.
func cleanSecret(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), ""))
}

// validSecret reports whether s is base32 of 16 to 32 characters (A-Z, 2-7).
func validSecret(s string) bool {
	if len(s) < 16 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}
