package contact

import (
	"strconv"
	"strings"

	"github.com/nyaruka/phonenumbers"
)

// Phone numbers (ticket 300, ADR 0026). A contact keeps a number as typed; for comparing, the number is read
// with the household's home country as its region and written in E.164. What cannot be read as a valid number
// (service numbers such as 116117) is compared by its digits.

// DefaultRegion is the region used when a household has no home country.
const DefaultRegion = "DE"

func regionOrDefault(region string) string {
	if region == "" {
		return DefaultRegion
	}
	return strings.ToUpper(region)
}

// E164 reads v as a number of the region (an ISO 3166-1 country such as "DE") and returns it in E.164 form
// ("+491713456789"); it is empty when v is empty or not a valid number of any country.
func E164(v, region string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	n, err := phonenumbers.Parse(v, regionOrDefault(region))
	if err != nil || !phonenumbers.IsValidNumber(n) {
		return ""
	}
	return phonenumbers.Format(n, phonenumbers.E164)
}

// Digits are the digits of v and nothing else.
func Digits(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NumberKey is what two numbers are compared by: the E.164 form, or for a number that cannot be read the
// digits. An empty value, or one without a digit, has no key (""): empty values never match.
func NumberKey(v, region string) string {
	if e := E164(v, region); e != "" {
		return e
	}
	return Digits(v)
}

// EmailKey is what two e-mail addresses are compared by: the address without surrounding spaces, in lower
// case. An empty value has no key: empty values never match.
func EmailKey(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

// CallingCode is the country code of a number as "+49", read with the region; empty when it cannot be read.
func CallingCode(v, region string) string {
	e := E164(v, region)
	if e == "" {
		return ""
	}
	n, err := phonenumbers.Parse(e, "")
	if err != nil {
		return ""
	}
	return "+" + strconv.Itoa(int(n.GetCountryCode()))
}
