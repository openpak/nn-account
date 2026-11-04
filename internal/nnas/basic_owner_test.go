package nnas

import (
	"encoding/base64"
	"testing"
)

// "Link an existing NNID" sends base64("username password"); 401 here is 102-2613.
func TestParseBasic(t *testing.T) {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	for in, want := range map[string][2]string{"WiiUTest hunter2": {"WiiUTest", "hunter2"}, "WiiUTest:hunter2": {"WiiUTest", "hunter2"}} {
		u, p, ok := parseBasic(enc(in))
		if !ok || u != want[0] || p != want[1] {
			t.Fatalf("%q -> %q %q %v", in, u, p, ok)
		}
	}
	for _, bad := range []string{enc("nopass"), enc(" x"), "!!"} {
		if _, _, ok := parseBasic(bad); ok {
			t.Fatalf("%q accepted", bad)
		}
	}
}
