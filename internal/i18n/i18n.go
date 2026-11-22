// Package i18n holds the user-facing text catalogues and language matching.
//
// Catalogues are flat JSON files in locales/, one per language, embedded in
// the binary. German is the default language.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Default is the language used when nothing better matches.
const Default = "de"

//go:embed locales/*.json
var files embed.FS

// Bundle holds all loaded catalogues.
type Bundle struct {
	catalogues map[string]map[string]string
	languages  []string
}

// Load reads all embedded catalogues.
func Load() (*Bundle, error) {
	entries, err := files.ReadDir("locales")
	if err != nil {
		return nil, err
	}
	b := &Bundle{catalogues: map[string]map[string]string{}}
	for _, e := range entries {
		lang := strings.TrimSuffix(e.Name(), ".json")
		data, err := files.ReadFile(path.Join("locales", e.Name()))
		if err != nil {
			return nil, err
		}
		cat := map[string]string{}
		if err := json.Unmarshal(data, &cat); err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", e.Name(), err)
		}
		b.catalogues[lang] = cat
		b.languages = append(b.languages, lang)
	}
	sort.Strings(b.languages)
	if _, ok := b.catalogues[Default]; !ok {
		return nil, fmt.Errorf("i18n: default language %q missing", Default)
	}
	return b, nil
}

// MustLoad returns the embedded catalogues, loaded once per process and shared:
// a Bundle is never changed after Load, and reading the JSON again for every
// server and place list cost tens of MB per test (ticket 238). It panics on a
// broken catalogue.
var MustLoad = sync.OnceValue(func() *Bundle {
	b, err := Load()
	if err != nil {
		panic(err)
	}
	return b
})

// Languages returns the available language codes, sorted.
func (b *Bundle) Languages() []string {
	return append([]string(nil), b.languages...)
}

// T translates key into lang. Unknown languages fall back to the default
// language; unknown keys return the key itself so gaps stay visible.
func (b *Bundle) T(lang, key string) string {
	if s, ok := b.catalogues[lang][key]; ok {
		return s
	}
	if s, ok := b.catalogues[Default][key]; ok {
		return s
	}
	return key
}

// MissingKeys reports, per language, keys that another catalogue has.
func (b *Bundle) MissingKeys() map[string][]string {
	all := map[string]bool{}
	for _, cat := range b.catalogues {
		for k := range cat {
			all[k] = true
		}
	}
	missing := map[string][]string{}
	for lang, cat := range b.catalogues {
		for k := range all {
			if _, ok := cat[k]; !ok {
				missing[lang] = append(missing[lang], k)
			}
		}
		sort.Strings(missing[lang])
	}
	for lang, keys := range missing {
		if len(keys) == 0 {
			delete(missing, lang)
		}
	}
	return missing
}

// Match picks the best available language for an Accept-Language header.
func (b *Bundle) Match(header string) string {
	best, bestQ := Default, 0.0
	for _, part := range strings.Split(header, ",") {
		tag, q := parseRange(part)
		if tag == "" || q <= 0 {
			continue
		}
		base, _, _ := strings.Cut(tag, "-")
		if _, ok := b.catalogues[base]; ok && q > bestQ {
			best, bestQ = base, q
		}
	}
	return best
}

func parseRange(s string) (tag string, q float64) {
	fields := strings.Split(strings.TrimSpace(s), ";")
	tag = strings.ToLower(strings.TrimSpace(fields[0]))
	q = 1
	for _, p := range fields[1:] {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && strings.TrimSpace(k) == "q" {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return "", 0
			}
			q = f
		}
	}
	return tag, q
}

// Message is user-facing text that is stored as a key plus parameters and
// translated when it is shown. The zero Message is empty text.
type Message struct {
	Key    string            `json:"key"`
	Params map[string]string `json:"params,omitempty"`
}

// Msg builds a Message from a key and name/value pairs.
func Msg(key string, kv ...string) Message {
	m := Message{Key: key}
	for i := 0; i+1 < len(kv); i += 2 {
		if m.Params == nil {
			m.Params = map[string]string{}
		}
		m.Params[kv[i]] = kv[i+1]
	}
	return m
}

// Format translates a Message into lang and fills its {name} placeholders.
// An empty Message gives "".
func (b *Bundle) Format(lang string, m Message) string {
	if m.Key == "" {
		return ""
	}
	text := b.T(lang, m.Key)
	pairs := make([]string, 0, 2*len(m.Params))
	for k, v := range m.Params {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}
