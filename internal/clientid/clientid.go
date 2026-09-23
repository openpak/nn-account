// Package clientid names what a person signed in from. Emulators send
// `X-OpenPak-Client: <client>/<version>` (cemu, azahar) on the requests that
// mint their tokens; a real console sends nothing. The name is stored with
// the NEX token, and nn-friends reads it back when that token logs in, so the
// same PNID shows "Wii U" from a console and "Cemu" from the emulator.
package clientid

import (
	"net/http"
	"strings"
)

// Header is the header emulators identify themselves with (the same one the
// Switch emulators send nx-baas).
const Header = "X-OpenPak-Client"

// Parse reads the client name from a header value, dropping the version.
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
