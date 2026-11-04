package nnas

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openpak/nn-account/internal/config"
	"openpak/nn-account/internal/store"
)

func serve(s *Server, method, target, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(method, target, strings.NewReader(body)))
	return w
}

func testServer() *Server { return New(nil, nil, &config.Config{NNASDomain: "account.test"}) }

func TestTimeZoneTableCoversEurope(t *testing.T) {
	if len(timeZones) < 100 {
		t.Fatalf("only %d countries", len(timeZones))
	}
	w := serve(testServer(), "GET", "/v1/api/content/time_zones/GB/en", "")
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, "<area>Europe/London</area>") || !strings.Contains(b, "<timezones><timezone>") {
		t.Fatalf("GB/en: %d %.200s", w.Code, b)
	}
	if strings.Count(b, "<timezone>") > 32 {
		t.Fatal("more than 32 zones crashes a Wii U")
	}
	// an unknown language falls back to the country's own list; an unknown country is refused
	if w := serve(testServer(), "GET", "/v1/api/content/time_zones/DE/xx", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<timezone>") {
		t.Fatalf("DE/xx: %d", w.Code)
	}
	if w := serve(testServer(), "GET", "/v1/api/content/time_zones/ZZ/en", ""); w.Code != 400 {
		t.Fatalf("ZZ: %d", w.Code)
	}
	if w := serve(testServer(), "GET", "/v1/api/content/time_zones/US/en", ""); !strings.Contains(w.Body.String(), "Eastern Time (US &amp; Canada)") {
		t.Fatal("US eastern missing")
	}
}

func TestAgreementsServeTheRequestedRegion(t *testing.T) {
	w := serve(testServer(), "GET", "/v1/api/content/agreements/Nintendo-Network-EULA/GB/@latest", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<country>GB</country>") || !strings.Contains(w.Body.String(), "<language>de</language>") {
		t.Fatalf("%d %.200s", w.Code, w.Body.String())
	}
	if w := serve(testServer(), "GET", "/v1/api/content/agreements/x/%3Cscript%3E/1", ""); !strings.Contains(w.Body.String(), "<country>US</country>") {
		t.Fatal("bad region not defaulted")
	}
}

func TestValidateEmailIsPost(t *testing.T) {
	s := testServer()
	r := httptest.NewRequest("POST", "/v1/api/support/validate/email", strings.NewReader("email=nobody"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "0103") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// Routes behind the access token refuse a request without one.
func TestPersonRoutesNeedAToken(t *testing.T) {
	s := testServer()
	for _, rt := range [][2]string{
		{"PUT", "/v1/api/people/@me"}, {"GET", "/v1/api/people/@me/emails"}, {"PUT", "/v1/api/people/@me/emails/@primary"},
		{"POST", "/v1/api/people/@me/devices/"}, // trailing slash, as the console sends it (404 was 102-2534)
	} {
		w := serve(s, rt[0], rt[1], "<person/>")
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "1105") {
			t.Errorf("%v: %d %s", rt, w.Code, w.Body.String())
		}
	}
}

func TestSetEmailIsRefusedCleanly(t *testing.T) {
	w := httptest.NewRecorder()
	testServer().handleSetEmail(w, httptest.NewRequest("PUT", "/", nil), nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "<code>1600</code>") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestEmailsXML(t *testing.T) {
	x := emailsXML(1800000000, "a@b.c", true)
	for _, want := range []string{"<address>a@b.c</address>", "<primary>Y</primary>", "<validated>Y</validated>", "<reachable>Y</reachable>"} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %s in %s", want, x)
		}
	}
	if !strings.Contains(emailsXML(1, "a@b.c", false), "<validated>N</validated>") {
		t.Error("unverified not N")
	}
}

func TestPersonUpdatePlan(t *testing.T) {
	p, err := readPerson(httptest.NewRecorder(), httptest.NewRequest("PUT", "/", strings.NewReader(
		`<person><gender>M</gender><region>2</region><country>gb</country><language>en</language><tz_name></tz_name><marketing_flag>Y</marketing_flag><off_device_flag>N</off_device_flag><password>secret1</password></person>`)))
	if err != nil {
		t.Fatal(err)
	}
	u := planPersonUpdate(p)
	if u["country"] != "GB" || u["region"] != int32(2) || u["gender"] != "M" || u["marketing_flag"] != true || u["off_device_flag"] != false {
		t.Fatalf("%v", u)
	}
	if _, ok := u["timezone_name"]; ok {
		t.Fatal("an empty tz_name (3DS) must not overwrite")
	}
	if _, ok := u["password"]; ok {
		t.Fatal("password must not be a column")
	}
	if ValidateConsolePassword("abc", "x") == nil || ValidateConsolePassword("goodpass1", "x") != nil {
		t.Fatal("password rule")
	}
}

func TestMiisNeedPids(t *testing.T) {
	if w := serve(testServer(), "GET", "/v1/api/miis", ""); w.Code != 400 || !strings.Contains(w.Body.String(), "1600") {
		t.Fatalf("%d", w.Code)
	}
}

func TestMiiXML(t *testing.T) {
	x := miiXML("https://account.test", &store.PNID{PID: 1800000001, Username: "someone", MiiName: "Some", MiiData: "AB\nCD"})
	if !strings.Contains(x, "<data>ABCD</data>") || !strings.Contains(x, "https://account.test/mii/1800000001/normal_face.png") || strings.Count(x, "<image>") != 8 {
		t.Fatal(x)
	}
}

func TestSupportBadInput(t *testing.T) {
	s := testServer()
	for _, c := range []struct{ method, target, code string }{
		{"PUT", "/v1/api/support/email_confirmation/abc/123456", "0130"},
		{"GET", "/v1/api/support/resend_confirmation", "0130"},
		{"GET", "/v1/api/support/send_confirmation/pin/nobody", "0103"},
		{"GET", "/v1/api/support/send_confirmation/pin/a@b.c", "1600"},
		{"GET", "/v1/api/support/forgotten_password/abc", "0113"},
	} {
		w := serve(s, c.method, c.target, "")
		if w.Code != 400 || !strings.Contains(w.Body.String(), c.code) {
			t.Errorf("%s %s: %d %s", c.method, c.target, w.Code, w.Body.String())
		}
	}
}
