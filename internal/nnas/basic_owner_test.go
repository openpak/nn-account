package nnas

import (
	"encoding/xml"
	"time"

	"encoding/base64"
	"openpak/nn-account/internal/store"
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

// devices/owner and @me/profile share this person; the console needs the upstream fields (102-2421 otherwise).
func TestPersonXMLUpstreamShape(t *testing.T) {
	p := &store.PNID{PID: 1570020176, Username: "WiiUTest", Country: "PT", Language: "en", Region: 4,
		TimezoneName: "Europe/Lisbon", MiiName: "WiiUTest", MiiData: "AwAA\r\n", MiiHash: "abc", CreationDate: time.Unix(0, 0)}
	x := personXML(p, "https://account.openpak.org", "wiiu@openpak.org", "1990-01-01", true)
	var v struct {
		UserID string `xml:"user_id"`
		Email  string `xml:"email>address"`
		Mii    string `xml:"mii>data"`
		Offset string `xml:"utc_offset"`
		TZ     string `xml:"tz_name"`
	}
	if err := xml.Unmarshal([]byte(x), &v); err != nil {
		t.Fatal(err)
	}
	if v.UserID != "WiiUTest" || v.Email != "wiiu@openpak.org" || v.Mii != "AwAA" || v.TZ != "Europe/Lisbon" || v.Offset == "" {
		t.Fatalf("%+v", v)
	}
	t.Log(v.Offset)
}
