package nnas

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"openpak/nn-account/internal/store"
)

// handleRegisterPerson ports POST /v1/api/people (console registration).
// Account creation is delegated to the core (PRD FR-1); the adapter owns the
// PNID/NEX identities and their core link.
func (s *Server) handleRegisterPerson(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}
	userID := r.FormValue("user_id")
	password := r.FormValue("password")
	email := strings.ToLower(r.FormValue("email.address"))
	miiName := r.FormValue("mii.name")
	miiData := r.FormValue("mii.data")
	country := r.FormValue("country")
	language := r.FormValue("language")
	timezoneName := r.FormValue("tz_name")
	region := r.FormValue("region")

	if userID == "" || password == "" || email == "" {
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}

	// Generate the NEX identity first (upstream ordering): NEX pid == PNID pid.
	pid, err := store.GeneratePID(r.Context(), s.pool)
	if err != nil {
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}

	// Delegate account creation to the core. The plaintext crosses once,
	// in memory, to the authenticated internal API (PRD §7); it is never
	// persisted or logged by the adapter.
	reg, err := s.core.RegisterAccount(r.Context(), "wiiu", email, password, userID, country, language)
	if err != nil {
		log.Printf("[POST] /v1/api/people: core registration failed: %v", err)
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}
	if reg.GetEmailTaken() {
		// Do not reveal email registration state to the console.
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}
	accountID := reg.GetAccountId()

	// Store the console credential transformation in the core's adapter
	// credential domain, exactly as consoles will send it at login.
	transformed := NintendoPasswordHash(password, pid)
	if err := s.core.SetAdapterCredential(r.Context(), "wiiu", accountID, transformed); err != nil {
		log.Printf("[POST] /v1/api/people: set credential failed: %v", err)
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}

	// Register the link as pending, then activate (core is authoritative).
	if err := s.core.ReserveAndActivateLink(r.Context(), "wiiu", strconv.FormatInt(pid, 10), accountID); err != nil {
		log.Printf("[POST] /v1/api/people: link failed: %v", err)
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}

	// NEX account (separate secret domain; cryptographic randomness).
	nexPassword := nintendoRandomPassword(16)
	owningPID := pid
	nex := &store.NEXAccount{
		PID: pid, OwningPID: &owningPID, Password: nexPassword,
		AccessLevel: 0, ServerAccessLevel: "prod", DeviceType: "wiiu",
	}
	pnid := &store.PNID{
		PID: pid, Username: userID, AccountID: accountID,
		AccessLevel: 0, ServerAccessLevel: "prod",
		MiiName: miiName, MiiData: miiData, MiiHash: randomHex(7),
		Country: defaultStr(country, "US"), Language: defaultStr(language, "en"),
		Region: defaultInt(region, 1), TimezoneName: defaultStr(timezoneName, "EST5EDT"),
	}
	ctx := r.Context()
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// PNID first: nex_accounts.owning_pid references pnids.
		if err := store.InsertPNID(ctx, tx, pnid); err != nil {
			return err
		}
		return store.InsertNEXAccount(ctx, tx, nex)
	}); err != nil {
		if store.IsUniqueViolation(err) {
			log.Printf("[POST] /v1/api/people: unique violation: %v", err)
			writeXMLErr(w, http.StatusBadRequest, "user_id", "0102", "user_id already exists")
			return
		}
		log.Printf("[POST] /v1/api/people: insert failed: %v", err)
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<person><pid>` + strconv.FormatInt(pid, 10) + `</pid></person>`))
}

// handleGetPersonByUsername ports GET /v1/api/people/:username (used by
// consoles to check NNID availability during registration).
func (s *Server) handleGetPersonByUsername(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	if username == "@me" || username == "" {
		writeXMLErr(w, http.StatusNotFound, "", "0008", "Not Found")
		return
	}
	_, err := store.GetPNIDByUsername(r.Context(), s.pool, username)
	if err != nil {
		writeXMLErr(w, http.StatusNotFound, "", "0008", "Not Found")
		return
	}
	// Username exists: upstream returns a minimal person payload.
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = w.Write([]byte(`<person><username>` + escapeXML(username) + `</username></person>`))
}

// handleProfile ports GET /v1/api/people/@me/profile with field minimization
// (FR-8): only console-required profile fields are returned.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<person>`)
	b.WriteString(`<pid>` + strconv.FormatInt(pnid.PID, 10) + `</pid>`)
	b.WriteString(`<username>` + escapeXML(pnid.Username) + `</username>`)
	b.WriteString(`<country>` + escapeXML(pnid.Country) + `</country>`)
	b.WriteString(`<language>` + escapeXML(pnid.Language) + `</language>`)
	b.WriteString(`<region>` + strconv.FormatInt(int64(pnid.Region), 10) + `</region>`)
	b.WriteString(`<timezone><name>` + escapeXML(pnid.TimezoneName) + `</name></timezone>`)
	b.WriteString(`<mii><name>` + escapeXML(pnid.MiiName) + `</name><primary>Y</primary><data>` + escapeXML(pnid.MiiData) + `</data></mii>`)
	b.WriteString(`<flag><active>Y</active><mark_for_spam>N</mark_for_spam></flag>`)
	b.WriteString(`</person>`)
	_, _ = w.Write([]byte(b.String()))
}

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func defaultInt(v string, def int32) int32 {
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return int32(n)
}
