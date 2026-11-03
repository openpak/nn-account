package nnas

import (
	"net/http"
	"strconv"
	"strings"

	"openpak/nn-account/internal/store"
)

// supportPNID loads the person a support route names; unknown or deleted is 0130.
func (s *Server) supportPNID(w http.ResponseWriter, r *http.Request, pid int64) *store.PNID {
	pnid, err := store.GetPNIDByPID(r.Context(), s.pool, pid)
	if err != nil || pnid.Deleted {
		writeXMLErr(w, http.StatusBadRequest, "", "0130", "PID has not been registered yet")
		return nil
	}
	return pnid
}

func (s *Server) emailVerified(r *http.Request, pnid *store.PNID) (bool, bool) {
	acct, err := s.core.GetAccount(r.Context(), pnid.AccountID)
	if err != nil {
		return false, false
	}
	return acct.GetEmailVerified(), true
}

const websiteEmail = "Confirm your email address on the OpenPak website"

// handleEmailConfirmation ports PUT /support/email_confirmation/{pid}/{code}. The core confirms
// an address through the link it mails, not a six digit code, so a verified account is
// already confirmed (200) and anything else has no code to match (0116).
func (s *Server) handleEmailConfirmation(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(r.PathValue("pid"), 10, 64)
	if err != nil {
		writeXMLErr(w, http.StatusBadRequest, "", "0130", "PID has not been registered yet")
		return
	}
	pnid := s.supportPNID(w, r, pid)
	if pnid == nil {
		return
	}
	verified, ok := s.emailVerified(r, pnid)
	if !ok {
		writeXMLErr(w, http.StatusServiceUnavailable, "", "1600", "Unable to process request")
		return
	}
	if !verified {
		writeXMLErr(w, http.StatusBadRequest, "", "0116", "Missing or invalid verification code")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleResendConfirmation ports GET /support/resend_confirmation (X-Nintendo-PID). The core
// sends its own verification mail; there is nothing for the adapter to resend, so an
// already verified account is fine and an unverified one is told where to confirm.
func (s *Server) handleResendConfirmation(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(r.Header.Get("X-Nintendo-PID"), 10, 64)
	if err != nil {
		writeXMLErr(w, http.StatusBadRequest, "", "0130", "PID has not been registered yet")
		return
	}
	pnid := s.supportPNID(w, r, pid)
	if pnid == nil {
		return
	}
	verified, ok := s.emailVerified(r, pnid)
	if !ok {
		writeXMLErr(w, http.StatusServiceUnavailable, "", "1600", "Unable to process request")
		return
	}
	if !verified {
		writeXMLErr(w, http.StatusBadRequest, "", "1600", websiteEmail)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleSendConfirmationPin ports GET /support/send_confirmation/pin/{email} (parental controls
// mail). The adapter sends no mail and does not look accounts up by address.
func (s *Server) handleSendConfirmationPin(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.PathValue("email"), "@") {
		writeXMLErr(w, http.StatusBadRequest, "email", "0103", "Email format is invalid")
		return
	}
	writeXMLErr(w, http.StatusBadRequest, "", "1600", "Unable to process request")
}

// handleForgottenPassword ports GET /support/forgotten_password/{pid}. Password recovery is the
// core's website flow (mailed link); the adapter cannot start it.
func (s *Server) handleForgottenPassword(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(r.PathValue("pid"), 10, 64)
	if err != nil {
		writeXMLErr(w, http.StatusBadRequest, "device_id", "0113", "Unauthorized device")
		return
	}
	if s.supportPNID(w, r, pid) == nil {
		return
	}
	writeXMLErr(w, http.StatusBadRequest, "", "1600", "Reset your password on the OpenPak website")
}

// handleMiis ports GET /v1/api/miis?pids=1,2: the Mii of each PID that exists.
func (s *Server) handleMiis(w http.ResponseWriter, r *http.Request) {
	input := r.URL.Query().Get("pids")
	if input == "" {
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}
	base := s.cfg.CDNBaseURL
	if s.cfg.NNASDomain != "" {
		base = "https://" + s.cfg.NNASDomain
	}
	var b strings.Builder
	n := 0
	b.WriteString(`<miis>`)
	for _, f := range strings.Split(input, ",") {
		pid, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64)
		if err != nil {
			continue
		}
		pnid, err := store.GetPNIDByPID(r.Context(), s.pool, pid)
		if err != nil || pnid.Deleted {
			continue
		}
		n++
		b.WriteString(miiXML(base, pnid))
	}
	b.WriteString(`</miis>`)
	if n == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	setNintendoHeaders(w)
	_, _ = w.Write([]byte(b.String()))
}

var miiImages = []struct{ typ, file string }{
	{"standard", "normal_face.png"}, {"frustrated_face", "frustrated.png"}, {"happy_face", "smile_open_mouth.png"},
	{"like_face", "wink_left.png"}, {"normal_face", "normal_face.png"}, {"puzzled_face", "sorrow.png"},
	{"surprised_face", "surprised_open_mouth.png"}, {"whole_body", "body.png"},
}

func miiXML(base string, p *store.PNID) string {
	pid := strconv.FormatInt(p.PID, 10)
	var b strings.Builder
	b.WriteString(`<mii><data>` + escapeXML(strings.NewReplacer("\r", "", "\n", "").Replace(p.MiiData)) + `</data><id>` + pid + `</id><images>`)
	for _, im := range miiImages {
		u := escapeXML(base + "/mii/" + pid + "/" + im.file)
		b.WriteString(`<image><cached_url>` + u + `</cached_url><id>` + pid + `</id><url>` + u + `</url><type>` + im.typ + `</type></image>`)
	}
	b.WriteString(`</images><name>` + escapeXML(p.MiiName) + `</name><pid>` + pid + `</pid><primary>Y</primary><user_id>` +
		escapeXML(p.Username) + `</user_id></mii>`)
	return b.String()
}
