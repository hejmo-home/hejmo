// Package iosprofile builds the iOS configuration profile that sets up a
// CalDAV account on an iPhone or iPad in one step (ADR 0017). It is pure: the
// caller creates the device token and passes everything in.
package iosprofile

import (
	"bytes"
	"crypto/rand"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"strconv"
)

// Params is everything the profile holds.
type Params struct {
	DeviceName    string // shown as the account's description
	Host          string // box host name or address, without port
	Port          int
	Username      string
	Token         string // the device token, the account's password
	PrincipalPath string // path of the person's CalDAV principal, e.g. /dav/{person}/
	Identifier    string // PayloadIdentifier of the profile: reinstalling the same one replaces it
	ProfileUUID   string
	PayloadUUID   string
}

// CalDAV renders an unsigned configuration profile with one
// com.apple.caldav.account payload (Apple's Configuration Profile Reference).
func CalDAV(p Params) ([]byte, error) {
	if p.Host == "" || p.Port <= 0 || p.Username == "" || p.Token == "" || p.Identifier == "" || p.ProfileUUID == "" || p.PayloadUUID == "" {
		return nil, errors.New("iosprofile: host, port, user name, token, identifier and UUIDs are required")
	}
	authority := p.Host
	if p.Port != 443 || net.ParseIP(p.Host) != nil {
		authority = net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	}
	var b bytes.Buffer
	str := func(key, value string) {
		fmt.Fprintf(&b, "\t\t\t<key>%s</key>\n\t\t\t<string>", key)
		_ = xml.EscapeText(&b, []byte(value))
		b.WriteString("</string>\n")
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
`)
	str("PayloadType", "com.apple.caldav.account")
	b.WriteString("\t\t\t<key>PayloadVersion</key>\n\t\t\t<integer>1</integer>\n")
	str("PayloadIdentifier", p.Identifier+".account")
	str("PayloadUUID", p.PayloadUUID)
	str("PayloadDisplayName", "Hejmo")
	str("CalDAVAccountDescription", p.DeviceName)
	str("CalDAVHostName", p.Host)
	fmt.Fprintf(&b, "\t\t\t<key>CalDAVPort</key>\n\t\t\t<integer>%d</integer>\n", p.Port)
	b.WriteString("\t\t\t<key>CalDAVUseSSL</key>\n\t\t\t<true/>\n")
	str("CalDAVUsername", p.Username)
	str("CalDAVPassword", p.Token)
	str("CalDAVPrincipalURL", "https://"+authority+p.PrincipalPath)
	b.WriteString("\t\t</dict>\n\t</array>\n\t<key>PayloadType</key>\n\t<string>Configuration</string>\n\t<key>PayloadVersion</key>\n\t<integer>1</integer>\n")
	for _, kv := range [][2]string{{"PayloadIdentifier", p.Identifier}, {"PayloadUUID", p.ProfileUUID}, {"PayloadDisplayName", "Hejmo: " + p.DeviceName},
		{"PayloadDescription", "Sets up the calendars of your Hejmo box on this device. It holds a password: install it, then delete the file."}} {
		fmt.Fprintf(&b, "\t<key>%s</key>\n\t<string>", kv[0])
		_ = xml.EscapeText(&b, []byte(kv[1]))
		b.WriteString("</string>\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes(), nil
}

// NewUUID returns a random (version 4) UUID in the upper-case form profiles use.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the system's random source failing is not recoverable
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
