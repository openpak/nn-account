package nnas

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// timezonesJSON is Pretendo's table: country -> language -> zones, as the consoles list them.
//
//go:embed timezones.json
var timezonesJSON []byte

type timeZone struct {
	Area      string `json:"area"`
	Language  string `json:"language"`
	Name      string `json:"name"`
	UTCOffset string `json:"utc_offset"`
	Order     string `json:"order"`
}

var timeZones = func() map[string]map[string][]timeZone {
	m := map[string]map[string][]timeZone{}
	if err := json.Unmarshal(timezonesJSON, &m); err != nil {
		panic("nnas: timezones.json: " + err.Error())
	}
	return m
}()

// zonesFor returns the zones for a country and language; an unknown language gets the
// country's first language (Pretendo's rule), an unknown country gets nil.
func zonesFor(country, language string) []timeZone {
	langs, ok := timeZones[strings.ToUpper(country)]
	if !ok {
		return nil
	}
	if z, ok := langs[language]; ok {
		return z
	}
	keys := make([]string, 0, len(langs))
	for k := range langs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if _, ok := langs["en"]; ok {
		return langs["en"]
	}
	return langs[keys[0]]
}

// handleTimeZones ports GET /content/time_zones/{country}/{language}.
func (s *Server) handleTimeZones(w http.ResponseWriter, r *http.Request) {
	zones := zonesFor(r.PathValue("country"), r.PathValue("language"))
	if zones == nil {
		writeXMLErr(w, http.StatusBadRequest, "country", "0002", "country format is invalid")
		return
	}
	setNintendoHeaders(w)
	var b strings.Builder
	b.WriteString(`<timezones>`)
	for _, z := range zones {
		b.WriteString(`<timezone><area>` + escapeXML(z.Area) + `</area><language>` + escapeXML(z.Language) +
			`</language><name>` + escapeXML(z.Name) + `</name><utc_offset>` + escapeXML(z.UTCOffset) +
			`</utc_offset><order>` + escapeXML(z.Order) + `</order></timezone>`)
	}
	b.WriteString(`</timezones>`)
	_, _ = w.Write([]byte(b.String()))
}

var agreementLanguages = []struct{ code, name string }{
	{"en", "English"}, {"de", "Deutsch"}, {"es", "Español"}, {"fr", "Français"},
	{"it", "Italiano"}, {"ja", "日本語"}, {"nl", "Nederlands"}, {"pt", "Português"}, {"ru", "Русский"},
}

const agreementText = "OpenPak Network services are supplied as they are, with no warranty. Your account is your OpenPak account; " +
	"what the services keep about you is shown and can be removed on the OpenPak website."

// handleAgreements ports GET /content/agreements/{type}/{region}/{version}: one OpenPak
// agreement per console language, for the country the console asked about (EUR and every
// other region get their own country, not Pretendo's fixed US entry).
func (s *Server) handleAgreements(w http.ResponseWriter, r *http.Request) {
	country := strings.ToUpper(r.PathValue("region"))
	if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
		country = "US"
	}
	setNintendoHeaders(w)
	var b strings.Builder
	b.WriteString(`<agreements>`)
	for _, l := range agreementLanguages {
		b.WriteString(`<agreement><country>` + country + `</country><language>` + l.code +
			`</language><language_name>` + l.name + `</language_name><publish_date>2014-09-29T20:07:35</publish_date>` +
			`<texts xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="chunkedStoredAgreementText">` +
			`<main_title><![CDATA[OpenPak Network Services Agreement]]></main_title>` +
			`<agree_text><![CDATA[I Accept]]></agree_text><non_agree_text><![CDATA[I Decline]]></non_agree_text>` +
			`<main_text index="1"><![CDATA[` + agreementText + `]]></main_text>` +
			`<sub_title><![CDATA[Privacy Policy]]></sub_title>` +
			`<sub_text index="1"><![CDATA[` + agreementText + `]]></sub_text></texts>` +
			`<type>NINTENDO-NETWORK-EULA</type><version>0300</version></agreement>`)
	}
	b.WriteString(`</agreements>`)
	_, _ = w.Write([]byte(b.String()))
}
