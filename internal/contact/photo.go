package contact

import (
	"encoding/base64"
	"strings"
	"unicode"
)

// Initials are the letters that stand in for a missing photo (ticket 297):
// the Hejmo name is split on white space and brackets, the words that start
// with a letter are kept, and the first letter of the first two is
// upper-cased. A one-word name gives one letter; a name without such a word
// gives none.
func Initials(hejmoName string) string {
	words := strings.FieldsFunc(hejmoName, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("()[]{}<>", r)
	})
	var out []rune
	for _, w := range words {
		first := []rune(w)[0]
		if !unicode.IsLetter(first) {
			continue
		}
		out = append(out, unicode.ToUpper(first))
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

// PhotoVCard3 is the PHOTO property of a vCard 3.0 for a JPEG: base64 in the
// parameter form of RFC 2426, folded at 75 octets with CRLF and a space. It has no
// line end of its own. Without a photo it is empty.
func PhotoVCard3(jpeg []byte) string {
	if len(jpeg) == 0 {
		return ""
	}
	return fold("PHOTO;ENCODING=b;TYPE=JPEG:" + base64.StdEncoding.EncodeToString(jpeg))
}

// PhotoVCard4 is the PHOTO property of a vCard 4.0 for a JPEG: a data URI
// (RFC 6350), folded like PhotoVCard3.
func PhotoVCard4(jpeg []byte) string {
	if len(jpeg) == 0 {
		return ""
	}
	return fold("PHOTO:data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg))
}

// fold breaks a content line into physical lines of at most 75 octets; every
// line after the first starts with one space (RFC 6350, section 3.2). The
// line holds ASCII only, so no character is cut in two.
func fold(line string) string {
	const limit = 75
	var b strings.Builder
	b.WriteString(line[:min(limit, len(line))])
	for rest := line[min(limit, len(line)):]; rest != ""; {
		n := min(limit-1, len(rest))
		b.WriteString("\r\n ")
		b.WriteString(rest[:n])
		rest = rest[n:]
	}
	return b.String()
}
