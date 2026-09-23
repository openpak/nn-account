package clientid

import (
	"net/http/httptest"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]string{
		"cemu/2.6":                          "cemu",
		"Azahar/2125.0":                     "azahar",
		" cemu ":                            "cemu",
		"":                                  "",
		"/1.0":                              "",
		"cemu 2.6":                          "",
		"<script>/1":                        "",
		"abcdefghijklmnopqrstuvwxyzabcdefg": "",
	} {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOf(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if got := Of(r, "wiiu"); got != "wiiu" {
		t.Fatalf("no header: %q, want the console", got)
	}
	r.Header.Set(Header, "cemu/2.6")
	if got := Of(r, "wiiu"); got != "cemu" {
		t.Fatalf("cemu header: %q", got)
	}
	r.Header.Set(Header, "bad value/1")
	if got := Of(r, "3ds"); got != "3ds" {
		t.Fatalf("bad header: %q, want the console", got)
	}
}
