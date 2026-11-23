package contact

import (
	"strconv"
	"strings"
)

// JoinNames writes names out for a sentence: "A", "A und B", "A, B und C". and is the word before the last name
// ("und" or "and"). Every name is written; there is never a "+3".
func JoinNames(names []string, and string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " " + and + " " + names[len(names)-1]
}

// Related is what the card of a person says about one contact (ticket 298): the contact and the word it is.
// ContactID is the UID of the contact's card ("urn:uuid:" and the id), ContactName its Hejmo name.
type Related struct{ ContactID, ContactName, Word string }

// The versions of a vCard that RelatedLines writes.
const (
	VCard3 = "3.0"
	VCard4 = "4.0"
)

// RelatedType is the TYPE of a RELATED property (RFC 6350) for a word, or "" where none fits: the words of the
// shipped suggestions that mean a friend, kin or a neighbour. Everything else keeps the word as its label only.
func RelatedType(word string) string {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "freund", "freundin", "friend":
		return "friend"
	case "oma", "opa", "tante", "onkel", "mutter", "vater", "schwester", "bruder",
		"grandma", "grandpa", "aunt", "uncle", "mother", "father", "sister", "brother":
		return "kin"
	case "nachbar", "nachbarin", "neighbor", "neighbour":
		return "neighbor"
	}
	return ""
}

// RelatedLines are the content lines (unfolded) that put the relations into the card of a person, for the version
// VCard4 or VCard3. In 4.0 each is RELATED;VALUE=uri[;TYPE=...];LABEL=<word>:urn:uuid:<contact id>. In 3.0 (the
// iOS address book) each is a pair itemN.X-ABRELATEDNAMES:<Hejmo name> and itemN.X-ABLabel:<word>; the items are
// numbered from firstItem on, so the caller can keep clear of the groups it uses for other properties.
func RelatedLines(version string, items []Related, firstItem int) []string {
	var out []string
	for i, it := range items {
		if version == VCard3 {
			g := "item" + strconv.Itoa(firstItem+i)
			out = append(out, g+".X-ABRELATEDNAMES:"+escapeText(it.ContactName), g+".X-ABLabel:"+escapeText(it.Word))
			continue
		}
		line := "RELATED;VALUE=uri"
		if t := RelatedType(it.Word); t != "" {
			line += ";TYPE=" + t
		}
		out = append(out, line+";LABEL="+paramValue(it.Word)+":urn:uuid:"+it.ContactID)
	}
	return out
}

// escapeText escapes a vCard text value: backslash, comma, semicolon and line breaks.
func escapeText(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ",", `\,`, ";", `\;`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(s)
}

// paramValue writes a parameter value: in double quotes when it holds ; : or , (a double quote itself cannot be written and goes).
func paramValue(s string) string {
	s = strings.NewReplacer(`"`, "", "\r", " ", "\n", " ").Replace(s)
	if strings.ContainsAny(s, ";:,") || strings.TrimSpace(s) != s || strings.Contains(s, " ") {
		return `"` + s + `"`
	}
	return s
}
