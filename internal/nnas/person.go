package nnas

import (
	"encoding/xml"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"openpak/nn-account/internal/profilecore"
	"openpak/nn-account/internal/store"
)

// Console password rules (shared with the website's console-password endpoint): 6 to 16
// printable ASCII characters, no spaces, and not containing the Network ID.
const (
	MinConsolePassword = 6
	MaxConsolePassword = 16
)

var (
	ErrPasswordLength = errors.New("password_length")
	ErrPasswordASCII  = errors.New("password_ascii")
	ErrPasswordIsID   = errors.New("password_is_id")
)

// ValidateConsolePassword checks a console password against the rules for a Network ID.
func ValidateConsolePassword(password, nnid string) error {
	if len(password) < MinConsolePassword || len(password) > MaxConsolePassword {
		return ErrPasswordLength
	}
	for i := 0; i < len(password); i++ {
		if c := password[i]; c < 0x21 || c > 0x7e {
			return ErrPasswordASCII
		}
	}
	if nnid != "" && strings.Contains(strings.ToLower(password), strings.ToLower(nnid)) {
		return ErrPasswordIsID
	}
	return nil
}

// personUpdate is the <person> document of PUT /v1/api/people/@me and the email documents.
type personUpdate struct {
	XMLName      xml.Name `xml:"person"`
	Gender       string   `xml:"gender"`
	Region       string   `xml:"region"`
	Country      string   `xml:"country"`
	Language     string   `xml:"language"`
	TZName       string   `xml:"tz_name"`
	Password     string   `xml:"password"`
	MarketingFlg string   `xml:"marketing_flag"`
	OffDeviceFlg string   `xml:"off_device_flag"`
}

// planPersonUpdate turns the document into column updates; empty fields leave the PNID alone
// (a 3DS sends an empty tz_name). Flags are Y or N.
func planPersonUpdate(p *personUpdate) map[string]any {
	u := map[string]any{}
	if v := strings.TrimSpace(p.Gender); v != "" {
		u["gender"] = v
	}
	if n, err := strconv.Atoi(strings.TrimSpace(p.Region)); err == nil {
		u["region"] = int32(n)
	}
	if v := strings.ToUpper(strings.TrimSpace(p.Country)); len(v) == 2 {
		u["country"] = v
	}
	if v := strings.TrimSpace(p.Language); v != "" {
		u["language"] = v
	}
	if v := strings.TrimSpace(p.TZName); v != "" {
		u["timezone_name"] = v
	}
	if v := strings.TrimSpace(p.MarketingFlg); v != "" {
		u["marketing_flag"] = v == "Y"
	}
	if v := strings.TrimSpace(p.OffDeviceFlg); v != "" {
		u["off_device_flag"] = v == "Y"
	}
	return u
}

func readPerson(w http.ResponseWriter, r *http.Request) (*personUpdate, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var p personUpdate
	if len(strings.TrimSpace(string(body))) == 0 {
		return &p, nil
	}
	if err := xml.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// handleUpdatePerson ports PUT /v1/api/people/@me. Country, language, region, time zone, gender and
// the two flags live on the PNID (Postgres). A password is the console password: it goes to the
// core's credential domain and to pnids.password_cache as NintendoPasswordHash(password, pid), as
// the website's console-password endpoint does, and every token of the PNID is dropped (as
// upstream), so the console signs in again with it. It is a separate console password; the
// OpenPak website password is not touched.
func (s *Server) handleUpdatePerson(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	p, err := readPerson(w, r)
	if err != nil {
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}
	if p.Password != "" {
		if err := ValidateConsolePassword(p.Password, pnid.Username); err != nil {
			writeXMLErr(w, http.StatusBadRequest, "password", "0002", "password format is invalid")
			return
		}
		hash := NintendoPasswordHash(p.Password, pnid.PID)
		if err := s.core.SetAdapterCredential(r.Context(), "wiiu", pnid.AccountID, hash); err != nil {
			log.Printf("[PUT] /v1/api/people/@me: core credential for pid %d: %v", pnid.PID, err)
			writeXMLErr(w, http.StatusServiceUnavailable, "", "1600", "Unable to process request")
			return
		}
		if err := store.SetConsolePasswordCredential(r.Context(), s.pool, pnid.PID, hash, "custom"); err != nil {
			log.Printf("[PUT] /v1/api/people/@me: store credential for pid %d: %v", pnid.PID, err)
			writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
			return
		}
	}
	if err := profilecore.SaveUpdates(r.Context(), s.pool, pnid.AccountID, pnid.PID, planPersonUpdate(p)); err != nil {
		log.Printf("[PUT] /v1/api/people/@me: update pid %d: %v", pnid.PID, err)
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}
	if p.Password != "" {
		_ = store.RevokeTokensForPID(r.Context(), s.pool, pnid.PID)
	}
	setNintendoHeaders(w)
	w.WriteHeader(http.StatusOK)
}

// emailsXML is the GET /people/@me/emails body. The address belongs to the core account.
func emailsXML(pid int64, address string, verified bool) string {
	yn := "N"
	if verified {
		yn = "Y"
	}
	return `<emails><email><address>` + escapeXML(address) + `</address><id>` + strconv.FormatInt(pid, 10) +
		`</id><parent>N</parent><primary>Y</primary><reachable>` + yn + `</reachable><type>DEFAULT</type>` +
		`<updated_by>USER</updated_by><validated>` + yn + `</validated></email></emails>`
}

// handleEmails ports GET /v1/api/people/@me/emails from the core account.
func (s *Server) handleEmails(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	acct, err := s.core.GetAccount(r.Context(), pnid.AccountID)
	if err != nil {
		writeXMLErr(w, http.StatusServiceUnavailable, "", "1600", "Unable to process request")
		return
	}
	setNintendoHeaders(w)
	_, _ = w.Write([]byte(emailsXML(pnid.PID, acct.GetEmail(), acct.GetEmailVerified())))
}

// handleSetEmail answers PUT /v1/api/people/@me/emails/@primary. The address is the core
// account's sign-in email, and the core changes it only for a signed-in website session that
// proves the OpenPak password and a mailed link; an adapter has no such call. Faking success
// would leave the console showing an address the account does not have, so it is refused.
func (s *Server) handleSetEmail(w http.ResponseWriter, _ *http.Request, _ *store.PNID) {
	writeXMLErr(w, http.StatusBadRequest, "email", "1600", "Change your email address on the OpenPak website")
}
