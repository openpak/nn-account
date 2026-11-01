package miistudio

import (
	"html"
	"strings"
	"unicode"
)

// favouriteColors are the twelve Mii favourite colours in Mii order.
var favouriteColors = [12]string{
	"#d21e2b", "#f07a14", "#f2c418", "#8cc63f", "#1f9d4b", "#2756c5",
	"#2fb6e8", "#ef6fa8", "#7a3fbf", "#7a5230", "#e6e9ee", "#2a2f38",
}

// Palette index 10 (white) and 2 (yellow) need dark lettering.
var darkInk = map[int]bool{2: true, 3: true, 10: true}

// FallbackColor picks the avatar colour: the Mii's favourite colour when its
// data parsed, otherwise a stable pick from the pid.
func FallbackColor(m *Mii, pid int64) int {
	if m != nil {
		return m.FavoriteColor
	}
	if pid < 0 {
		pid = -pid
	}
	return int(pid % 12)
}

// Initial returns the first letter or digit of a name, upper-cased, or "?".
func Initial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return "?"
}

// FallbackSVG draws OpenPak's own avatar: a disc in the favourite colour, a
// cyan selection ring (the console-UI cursor colour of openpak.org) and the
// initial. It is original artwork and never derived from Nintendo's renders.
func FallbackSVG(initial string, colorIndex int) []byte {
	if colorIndex < 0 || colorIndex >= len(favouriteColors) {
		colorIndex = 0
	}
	ink := "#ffffff"
	if darkInk[colorIndex] {
		ink = "#20262f"
	}
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128" width="128" height="128" role="img" aria-label="`)
	b.WriteString(html.EscapeString(initial))
	b.WriteString(`"><circle cx="64" cy="64" r="60" fill="` + favouriteColors[colorIndex] + `" stroke="#0094be" stroke-width="6"/>`)
	b.WriteString(`<text x="64" y="64" text-anchor="middle" dominant-baseline="central" font-family="Inter,Helvetica,Arial,sans-serif" font-size="64" font-weight="700" fill="` + ink + `">`)
	b.WriteString(html.EscapeString(initial))
	b.WriteString(`</text></svg>`)
	return []byte(b.String())
}
