package nnas

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"openpak/nn-account/internal/store"
)

// handleDevices echoes the console-identifying headers back as the device
// list (upstream GET /@me/devices: consoles self-identify; the server does
// not persist advertised attributes — ported behavior).
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	deviceID := r.Header.Get("X-Nintendo-Device-ID")
	acceptLanguage := r.Header.Get("Accept-Language")
	platformID := r.Header.Get("X-Nintendo-Platform-ID")
	region := r.Header.Get("X-Nintendo-Region")
	serial := r.Header.Get("X-Nintendo-Serial-Number")
	systemVersion := r.Header.Get("X-Nintendo-System-Version")

	if deviceID == "" || acceptLanguage == "" || platformID == "" || region == "" || serial == "" || systemVersion == "" {
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}
	setNintendoHeaders(w)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<devices><device><device_id>` + escapeXML(deviceID) +
		`</device_id><language>` + escapeXML(acceptLanguage) +
		`</language><updated>` + time.Now().UTC().Format("2006-01-02T15:04:05") +
		`</updated><pid>` + strconv.FormatInt(pnid.PID, 10) +
		`</pid><platform_id>` + escapeXML(platformID) +
		`</platform_id><region>` + escapeXML(region) +
		`</region><serial_number>` + escapeXML(serial) +
		`</serial_number><status>ACTIVE</status><system_version>` + escapeXML(systemVersion) +
		`</system_version><type>RETAIL</type><updated_by>USER</updated_by></device></devices>`))
}

// handlePostDevices ports POST /@me/devices: returns the profile projection.
func (s *Server) handlePostDevices(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	setNintendoHeaders(w)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(profileXML(pnid)))
}

// handleDevicesOwner ports GET /@me/devices/owner (same as profile).
func (s *Server) handleDevicesOwner(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	s.handleProfile(w, r, pnid)
}

// handleDevicesStatus ports GET /@me/devices/status.
func (s *Server) handleDevicesStatus(w http.ResponseWriter, _ *http.Request, _ *store.PNID) {
	setNintendoHeaders(w)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<devices><device><status>ACTIVE</status></device></devices>`))
}

// handleInactivateDevice ports PUT /@me/devices/@current/inactivate: an
// empty 200 with standard headers (upstream behavior).
func (s *Server) handleInactivateDevice(w http.ResponseWriter, _ *http.Request, _ *store.PNID) {
	setNintendoHeaders(w)
	w.WriteHeader(http.StatusOK)
}

// handleSetPrimaryMii ports PUT /@me/miis/@primary: persists Mii data on the
// adapter's PNID record (Nintendo-specific profile asset, PRD §5).
func (s *Server) handleSetPrimaryMii(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	miiName := r.FormValue("mii.name")
	miiData := r.FormValue("mii.data")
	if miiName == "" || miiData == "" {
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}
	if err := store.UpdatePNIDMii(r.Context(), s.pool, pnid.PID, miiName, miiData); err != nil {
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}
	setNintendoHeaders(w)
	w.WriteHeader(http.StatusOK)
}

// handleDeletion ports POST /@me/deletion: delegates to the core's
// RequestAccountDeletion (access disabled immediately; FR-8 stage one).
func (s *Server) handleDeletion(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	if err := s.core.RequestAccountDeletion(r.Context(), pnid.AccountID); err != nil {
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}
	// Revoke all local tokens for this PID (event loop will also catch it).
	_ = store.RevokeTokensForPID(r.Context(), s.pool, pnid.PID)
	setNintendoHeaders(w)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<person><pid>` + strconv.FormatInt(pnid.PID, 10) + `</pid></person>`))
}

// handleMappedIDs ports GET /admin/mapped_ids (PID ↔ username resolution).
func (s *Server) handleMappedIDs(w http.ResponseWriter, r *http.Request) {
	inputType := r.URL.Query().Get("input_type")
	outputType := r.URL.Query().Get("output_type")
	input := r.URL.Query().Get("input")
	if inputType == "" || outputType == "" || input == "" {
		writeXMLErr(w, http.StatusBadRequest, "", "0002", "Invalid input")
		return
	}
	var pid int64
	var username string
	switch {
	case inputType == "pid" && outputType == "user_id":
		pid = parseInt64(input)
		pnid, err := store.GetPNIDByPID(r.Context(), s.pool, pid)
		if err != nil {
			writeXMLErr(w, http.StatusNotFound, "", "0008", "Not Found")
			return
		}
		username = pnid.Username
	case inputType == "user_id" && outputType == "pid":
		pnid, err := store.GetPNIDByUsername(r.Context(), s.pool, input)
		if err != nil {
			writeXMLErr(w, http.StatusNotFound, "", "0008", "Not Found")
			return
		}
		pid, username = pnid.PID, pnid.Username
	default:
		writeXMLErr(w, http.StatusBadRequest, "", "0002", "Invalid input")
		return
	}
	setNintendoHeaders(w)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<mapped_ids><mapped_id><pid>` + strconv.FormatInt(pid, 10) +
		`</pid><user_id>` + escapeXML(username) + `</user_id></mapped_id></mapped_ids>`))
}

// handleAgreements ports GET /content/agreements (OpenPak-authored content).
func (s *Server) handleAgreements(w http.ResponseWriter, r *http.Request) {
	setNintendoHeaders(w)
	_, _ = w.Write([]byte(`<agreements><agreement><version>1</version><country></country>` +
		`<agreement_text>OpenPak Network Terms of Service apply.</agreement_text>` +
		`</agreement></agreements>`))
}

// handleTimeZones ports GET /content/time_zones with a minimal, valid list
// (consoles select from this; any valid entry works).
func (s *Server) handleTimeZones(w http.ResponseWriter, r *http.Request) {
	setNintendoHeaders(w)
	var b strings.Builder
	b.WriteString(`<time_zones><time_zone><area>EST5EDT</area><language>en</language>` +
		`<name>Eastern Time (US &amp; Canada)</name><order>11</order><utc_offset>-14400</utc_offset></time_zone>`)
	b.WriteString(`<time_zone><area>PST8PDT</area><language>en</language>` +
		`<name>Pacific Time (US &amp; Canada)</name><order>3</order><utc_offset>-28800</utc_offset></time_zone>`)
	b.WriteString(`</time_zones>`)
	_, _ = w.Write([]byte(b.String()))
}

func setNintendoHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Server", "Nintendo 3DS (http)")
	w.Header().Set("X-Nintendo-Date", strconv.FormatInt(time.Now().UnixMilli(), 10))
}

func profileXML(p *store.PNID) string {
	var b strings.Builder
	b.WriteString(`<person>`)
	b.WriteString(`<pid>` + strconv.FormatInt(p.PID, 10) + `</pid>`)
	b.WriteString(`<username>` + escapeXML(p.Username) + `</username>`)
	b.WriteString(`<country>` + escapeXML(p.Country) + `</country>`)
	b.WriteString(`<language>` + escapeXML(p.Language) + `</language>`)
	b.WriteString(`<region>` + strconv.FormatInt(int64(p.Region), 10) + `</region>`)
	b.WriteString(`<timezone><name>` + escapeXML(p.TimezoneName) + `</name></timezone>`)
	b.WriteString(`<mii><name>` + escapeXML(p.MiiName) + `</name><primary>Y</primary><data>` + escapeXML(p.MiiData) + `</data></mii>`)
	b.WriteString(`</person>`)
	return b.String()
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
