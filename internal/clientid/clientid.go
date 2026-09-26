// Package clientid names what a person signed in from. Emulators send
// `X-OpenPak-Client: <client>/<version> (<os>)` (cemu, azahar) on the requests
// that mint their tokens; a real console sends nothing. The name and the OS
// are stored with the NEX token, and nn-friends reads them back when that
// token logs in, so the same PNID shows "Wii U" from a console and "Cemu" from
// the emulator, and the core can count playtime per emulator and OS.
package clientid

import (
	"net/http"
	"strings"
)

// Header is the header emulators identify themselves with (the same one the
// Switch emulators send nx-baas).
const Header = "X-OpenPak-Client"

// Parse reads the client name from a header value, dropping the version and
// the OS.
// Anything that is not a short lower-case token is no answer: it ends up in
// the core and on other people's screens.
func Parse(v string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(v), "/")
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > 32 {
		return ""
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return ""
		}
	}
	return name
}

// Of is the client a request came from: the header's client, otherwise the
// console the surface serves ("wiiu" for NNAS, "3ds" for NASC).
func Of(r *http.Request, console string) string {
	if c := Parse(r.Header.Get(Header)); c != "" {
		return c
	}
	return console
}

// oses are the OS names the core accepts; anything else is no answer.
var oses = map[string]bool{"windows": true, "macos": true, "linux": true, "android": true, "ios": true}

// ParseOS reads the OS from the parenthesised suffix of a header value
// ("cemu/2.6 (linux)"), lower-cased. Missing or unknown is "".
func ParseOS(v string) string {
	_, rest, ok := strings.Cut(v, "(")
	if !ok {
		return ""
	}
	os, _, ok := strings.Cut(rest, ")")
	if !ok {
		return ""
	}
	os = strings.ToLower(strings.TrimSpace(os))
	if !oses[os] {
		return ""
	}
	return os
}

// OSOf is the OS a request's emulator said it runs on; "" for a console.
func OSOf(r *http.Request) string {
	return ParseOS(r.Header.Get(Header))
}
