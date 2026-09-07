package nnas

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"openpak/nn-account/internal/store"
)

// Wii U account settings applet client ID (upstream constant).
const wiiuSettingsClientID = "3f3928cc6f780638d360f0485cef973f"

// settingsAuth authenticates the Wii U settings applet via its independent
// service token (X-Nintendo-Service-Token), bound to client_id + PID.
func (s *Server) settingsAuth(next func(http.ResponseWriter, *http.Request, *store.PNID)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Nintendo-Service-Token")
		if token == "" {
			w.WriteHeader(http.StatusGatewayTimeout) // upstream 504
			return
		}
		clientID, pid, _, issued, expires, err := store.GetIndependentServiceToken(r.Context(), s.pool, token)
		if err != nil || clientID != wiiuSettingsClientID || !expires.After(time.Now()) {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		_ = issued
		pnid, err := store.GetPNIDByPID(r.Context(), s.pool, pid)
		if err != nil || pnid.Deleted {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		// Core authority (review P1 #2): a live local token is not
		// permission — the account and its link must still be active
		// (FR-7/FR-8: deletion starts → settings freeze immediately).
		acct, err := s.core.GetAccount(r.Context(), pnid.AccountID)
		if err != nil || acct.GetStatus() != 1 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		if _, err := s.core.GetActiveLink(r.Context(), "wiiu", strconv.FormatInt(pnid.PID, 10)); err != nil {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		next(w, r, pnid)
	}
}

// handleSettingsProfile renders the NNID settings page (OpenPak-authored
// template, functionally equivalent to upstream's EJS; recorded in M0).
func (s *Server) handleSettingsProfile(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	notice := r.URL.Query().Get("notice")
	_, _ = w.Write([]byte(settingsPageHTML(pnid, notice)))
}

// handleSettingsUpdate applies adapter-owned profile fields from the
// settings applet. Email changes are core-owned and are NOT accepted here;
// the page directs users to the OpenPak website (recorded in M0).
func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/v1/api/account_settings/ui/profile", http.StatusNotFound)
		return
	}
	tzName := r.FormValue("tz_name")
	regionStr := r.FormValue("region")
	country := r.FormValue("country")
	miiName := r.FormValue("mii.name")
	miiData := r.FormValue("mii.data")

	updates := map[string]any{}
	if tzName != "" {
		updates["timezone_name"] = tzName
	}
	if country != "" {
		updates["country"] = country
	}
	if regionStr != "" {
		if n, err := strconv.Atoi(regionStr); err == nil {
			updates["region"] = int32(n)
		}
	}
	if miiName != "" && miiData != "" {
		updates["mii_name"] = miiName
		updates["mii_data"] = miiData
	}
	if err := store.UpdatePNIDSettings(r.Context(), s.pool, pnid.PID, updates); err != nil {
		http.Redirect(w, r, "/v1/api/account_settings/ui/profile?notice=error", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/v1/api/account_settings/ui/profile?notice=saved", http.StatusFound)
}

// handleSettingsMiiImage proxies the Mii image from the CDN: the settings
// applet enforces a strict domain whitelist, so the adapter re-serves it.
func (s *Server) handleSettingsMiiImage(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	face := r.PathValue("face")
	if !validPIDPath(pid) || !validFace(face) {
		http.NotFound(w, r)
		return
	}
	resp, err := http.Get(s.cfg.CDNBaseURL + "/mii/" + pid + "/" + face + ".tga") // #nosec G107 -- base URL is operator config
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "max-age=300")
	w.WriteHeader(resp.StatusCode)
	limited := io.LimitReader(resp.Body, 1<<20) // 1 MiB bound
	_, _ = w.Write(readAll(limited))
}

func validPIDPath(pid string) bool {
	if pid == "" || len(pid) > 10 {
		return false
	}
	for _, c := range pid {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func validFace(face string) bool {
	switch face {
	case "normal_face", "smile_open_mouth", "sorrow", "surprise_open_mouth",
		"wink_left", "frustrated":
		return true
	}
	return false
}

func readAll(r io.Reader) []byte {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf
}

func settingsPageHTML(p *store.PNID, notice string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">` +
		`<title>OpenPak ID Settings</title>` +
		`<style>body{font-family:sans-serif;background:#f4f4f4;margin:0;padding:1em}` +
		`form{background:#fff;padding:1em;max-width:480px;margin:auto;border-radius:8px}` +
		`label{display:block;margin-top:.7em;font-weight:bold}input,select{width:100%;padding:.4em}` +
		`.notice{max-width:480px;margin:0 auto 1em;padding:.6em;border-radius:6px}` +
		`.saved{background:#dff5df}.error{background:#f5dfdf}</style></head><body>`)
	switch notice {
	case "saved":
		b.WriteString(`<div class="notice saved">Profile saved.</div>`)
	case "error":
		b.WriteString(`<div class="notice error">Could not save; try again.</div>`)
	}
	b.WriteString(`<form method="post" action="/v1/api/account_settings/update">`)
	b.WriteString(`<h1>OpenPak ID settings</h1>`)
	b.WriteString(`<p>Account: ` + escapeXML(p.Username) + ` (PID ` + strconv.FormatInt(p.PID, 10) + `)</p>`)
	b.WriteString(`<label for="mii_name">Mii name</label><input id="mii_name" name="mii.name" value="` + escapeXML(p.MiiName) + `">`)
	b.WriteString(`<label for="mii_data">Mii data</label><textarea id="mii_data" name="mii.data" rows="3">` + escapeXML(p.MiiData) + `</textarea>`)
	b.WriteString(`<label for="country">Country</label><input id="country" name="country" value="` + escapeXML(p.Country) + `">`)
	b.WriteString(`<label for="tz_name">Time zone</label><input id="tz_name" name="tz_name" value="` + escapeXML(p.TimezoneName) + `">`)
	b.WriteString(`<label for="region">Region</label><input id="region" name="region" type="number" value="` + strconv.FormatInt(int64(p.Region), 10) + `">`)
	b.WriteString(`<p>Email address changes are managed on the OpenPak website.</p>`)
	b.WriteString(`<button type="submit">Save</button></form></body></html>`)
	return b.String()
}

// WiiUSettingsClientID exposes the settings applet client ID for tests.
func WiiUSettingsClientID() string { return wiiuSettingsClientID }
