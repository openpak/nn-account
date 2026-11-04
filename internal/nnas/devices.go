package nnas

import (
	"encoding/xml"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"openpak/nn-account/internal/miicore"
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

// handlePostDevices ports POST /@me/devices: the same person as @me/profile.
func (s *Server) handlePostDevices(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	s.handleProfile(w, r, pnid)
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
	miiName, miiData := parseMiiBody(r)
	if miiName == "" || miiData == "" {
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}
	// The Mii belongs to the account: it goes to the core, where the Wii and the
	// Switch read it too. Only if the core cannot take it does it stay in pnids.
	if !miicore.Save(r.Context(), s.core, s.pool, pnid.PID, pnid.AccountID, miiData) {
		log.Printf("[PUT] /v1/api/people/@me/miis/@primary: pid %d: Mii kept locally, core did not take it", pnid.PID)
	}
	if err := store.UpdatePNIDMiiName(r.Context(), s.pool, pnid.PID, miiName); err != nil {
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}
	setNintendoHeaders(w)
	w.WriteHeader(http.StatusOK)
}

// parseMiiBody reads the console's XML body <mii><name/><primary/><data/></mii>
// (upstream request.body.mii); flat mii.name/mii.data form fields are kept for tooling.
func parseMiiBody(r *http.Request) (name, data string) {
	if strings.Contains(r.Header.Get("Content-Type"), "xml") || r.Header.Get("Content-Type") == "" {
		var m struct {
			Name string `xml:"name"`
			Data string `xml:"data"`
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if xml.Unmarshal(body, &m) == nil && m.Name != "" {
			return m.Name, strings.Join(strings.Fields(m.Data), "")
		}
	}
	return r.FormValue("mii.name"), r.FormValue("mii.data")
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

// handleDeviceStatus ports GET /v1/api/devices/@current/status. Its use is unknown upstream too; an empty
// device element lets the console's NNID link carry on (a 404 here is error 102-2534).
func (s *Server) handleDeviceStatus(w http.ResponseWriter, _ *http.Request) {
	setNintendoHeaders(w)
	_, _ = w.Write([]byte(`<?xml version="1.0"?><device></device>`))
}

func setNintendoHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Server", "Nintendo 3DS (http)")
	w.Header().Set("X-Nintendo-Date", strconv.FormatInt(time.Now().UnixMilli(), 10))
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
