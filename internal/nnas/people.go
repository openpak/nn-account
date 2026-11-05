package nnas

import (
	"encoding/xml"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"openpak/nn-account/internal/store"
)

// nascPerson mirrors the XML `<person>` document consoles POST to
// /v1/api/people (upstream parses the same nested shape via its XML body
// middleware). Flat form fields are accepted as a fallback.
type nascPerson struct {
	XMLName  xml.Name `xml:"person"`
	UserID   string   `xml:"user_id"`
	Password string   `xml:"password"`
	Country  string   `xml:"country"`
	Language string   `xml:"language"`
	Region   string   `xml:"region"`
	TZName   string   `xml:"tz_name"`
	Email    struct {
		Address string `xml:"address"`
	} `xml:"email"`
	Mii struct {
		Name string `xml:"name"`
		Data string `xml:"data"`
	} `xml:"mii"`
}

// handleRegisterPerson ports POST /v1/api/people (console registration).
// Account creation is delegated to the core (PRD FR-1); the adapter owns the
// PNID/NEX identities and their core link.
func (s *Server) handleRegisterPerson(w http.ResponseWriter, r *http.Request) {
	userID, password, email, _, miiData, country, language, timezoneName, region, parseErr := s.parsePersonBody(w, r)
	if parseErr != nil {
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}

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

	// Duplicate usernames are the common failure: pre-check so the common
	// case never creates core state (review P1 #6).
	if _, err := store.GetPNIDByUsername(r.Context(), s.pool, userID); err == nil {
		writeXMLErr(w, http.StatusBadRequest, "user_id", "0102", "user_id already exists")
		return
	}

	// Delegate account creation to the core. The plaintext crosses once,
	// in memory, to the authenticated internal API (PRD §7); it is never
	// persisted or logged by the adapter.
	reg, err := s.core.RegisterAccount(r.Context(), "wiiu", email, password, userID, "", country, language)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists && status.Convert(err).Message() == "handle_taken" {
			writeXMLErr(w, http.StatusBadRequest, "user_id", "0100", "Account ID already exists")
			return
		}
		if status.Code(err) == codes.InvalidArgument && strings.HasPrefix(status.Convert(err).Message(), "handle_") {
			writeXMLErr(w, http.StatusBadRequest, "user_id", "0002", "user_id is not allowed; use 6-15 letters and digits, starting with a letter")
			return
		}
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
	// The transformed secret IS the account.dat password cache (the console
	// derived it from the plaintext with the same transformation); keeping it
	// here means the emulator surface returns the same identity the console
	// holds instead of minting a second credential over this one.
	cacheHex := transformed

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
		MiiName: store.ProfileName(userID), MiiData: miiData, MiiHash: randomHex(7),
		Country: defaultStr(country, "US"), Language: strings.TrimSpace(language),
		Region: defaultInt(region, 1), TimezoneName: strings.TrimSpace(timezoneName),
		PasswordCache: cacheHex,
	}
	ctx := r.Context()
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// PNID first: nex_accounts.owning_pid references pnids.
		if err := store.InsertPNID(ctx, tx, pnid); err != nil {
			return err
		}
		return store.InsertNEXAccount(ctx, tx, nex)
	}); err != nil {
		// Compensation (review P1 #6): the core account + link were already
		// committed. Never leave that state orphaned — unlink the subject
		// and start core deletion so the account is unusable and the
		// username frees up on cleanup. Durable via core events.
		if err := s.core.UnlinkBySubject(ctx, "wiiu", strconv.FormatInt(pid, 10)); err != nil {
			log.Printf("[POST] /v1/api/people: COMPENSATION unlink failed for account %s: %v", accountID, err)
		}
		if err := s.core.RequestAccountDeletion(ctx, accountID); err != nil {
			log.Printf("[POST] /v1/api/people: COMPENSATION deletion failed for account %s: %v", accountID, err)
		}
		if store.IsUniqueViolation(err) {
			log.Printf("[POST] /v1/api/people: unique violation: %v", err)
			writeXMLErr(w, http.StatusBadRequest, "user_id", "0102", "user_id already exists")
			return
		}
		log.Printf("[POST] /v1/api/people: insert failed: %v", err)
		writeXMLErr(w, http.StatusBadRequest, "Bad Request", "1600", "Unable to process request")
		return
	}

	// Publish the NNID on the link so the website and the app can show it and
	// people can add each other by it. Best effort: the account works without
	// it, and the startup pass in main publishes whatever this missed.
	if err := s.core.PublishNNID(ctx, pid, userID); err != nil {
		log.Printf("[POST] /v1/api/people: publish NNID for pid %d: %v", pid, err)
	}

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<person><pid>` + strconv.FormatInt(pid, 10) + `</pid></person>`))
}

// handleGetPersonByUsername ports GET /v1/api/people/:username — the NNID
// availability check. Upstream semantics (review P2 #10): AVAILABLE name →
// empty 200; TAKEN name → 400 with error 0100 "Account ID already exists".
func (s *Server) handleGetPersonByUsername(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	if username == "@me" || username == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	check, err := s.core.CheckHandle(r.Context(), username)
	if err != nil {
		writeXMLErr(w, http.StatusServiceUnavailable, "", "1600", "Unable to process request")
		return
	}
	if !check.GetValid() {
		writeXMLErr(w, http.StatusBadRequest, "user_id", "0002", "user_id is not allowed")
		return
	}
	if !check.GetAvailable() {
		writeXMLErr(w, http.StatusBadRequest, "user_id", "0100", "Account ID already exists")
		return
	}
	_, err = store.GetPNIDByUsername(r.Context(), s.pool, username)
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		writeXMLErr(w, http.StatusServiceUnavailable, "", "1600", "Unable to process request")
		return
	}
	writeXMLErr(w, http.StatusBadRequest, "", "0100", "Account ID already exists")
}

// handleProfile ports GET /v1/api/people/@me/profile (also devices/owner) in the
// upstream getPNIDProfileJSONByPID shape. The console parses every field: a
// cut-down person made "Link an existing ID" fail with 102-2421.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	var email, birth string
	verified := false
	if acct, err := s.core.GetAccount(r.Context(), pnid.AccountID); err == nil {
		email, birth, verified = acct.GetEmail(), acct.GetBirthday(), acct.GetEmailVerified()
	}
	base := s.cfg.CDNBaseURL
	if s.cfg.NNASDomain != "" {
		base = "https://" + s.cfg.NNASDomain
	}
	setNintendoHeaders(w)
	_, _ = w.Write([]byte(`<?xml version="1.0"?>` + personXML(pnid, base, email, birth, verified)))
}

func personXML(p *store.PNID, base, email, birth string, verified bool) string {
	yn := func(b bool) string {
		if b {
			return "Y"
		}
		return "N"
	}
	if birth == "" {
		birth = "1990-01-01"
	}
	pid := strconv.FormatInt(p.PID, 10)
	created := p.CreationDate.UTC().Format("2006-01-02T15:04:05")
	img := escapeXML(base + "/mii/" + pid + "/normal_face.png")
	validated := ""
	if verified {
		validated = created
	}
	var b strings.Builder
	b.WriteString(`<person><active_flag>Y</active_flag><birth_date>` + escapeXML(birth) + `</birth_date>`)
	b.WriteString(`<country>` + escapeXML(p.Country) + `</country><create_date>` + created + `</create_date>`)
	b.WriteString(`<device_attributes/><gender>M</gender><language>` + escapeXML(p.Language) + `</language>`)
	b.WriteString(`<updated>` + created + `</updated><marketing_flag>N</marketing_flag><off_device_flag>N</off_device_flag>`)
	b.WriteString(`<pid>` + pid + `</pid>`)
	b.WriteString(`<email><address>` + escapeXML(email) + `</address><id>` + pid + `</id><parent>N</parent><primary>Y</primary>` +
		`<reachable>Y</reachable><type>DEFAULT</type><updated_by>USER</updated_by><validated>` + yn(verified) +
		`</validated><validated_date>` + validated + `</validated_date></email>`)
	b.WriteString(`<mii><status>COMPLETED</status><data>` + escapeXML(strings.NewReplacer("\r", "", "\n", "").Replace(p.MiiData)) +
		`</data><id>` + pid + `</id><mii_hash>` + escapeXML(p.MiiHash) + `</mii_hash><mii_images><mii_image><cached_url>` + img +
		`</cached_url><id>` + pid + `</id><url>` + img + `</url><type>standard</type></mii_image></mii_images><name>` +
		escapeXML(p.MiiName) + `</name><primary>Y</primary></mii>`)
	b.WriteString(`<region>` + strconv.FormatInt(int64(p.Region), 10) + `</region><tz_name>` + escapeXML(p.TimezoneName) +
		`</tz_name><user_id>` + escapeXML(p.Username) + `</user_id><utc_offset>` + utcOffset(p) + `</utc_offset></person>`)
	return b.String()
}

// utcOffset finds the PNID's zone in the console time-zone table (seconds), 0 if absent.
func utcOffset(p *store.PNID) string {
	for _, z := range zonesFor(p.Country, p.Language) {
		if z.Name == p.TimezoneName {
			return z.UTCOffset
		}
	}
	return "0"
}

// parsePersonBody extracts the registration document. Consoles POST XML
// `<person>` (upstream shape); flat form encoding is accepted for tooling.
func (s *Server) parsePersonBody(w http.ResponseWriter, r *http.Request) (userID, password, email, miiName, miiData, country, language, tzName, region string, err error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "xml") {
		body, rerr := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if rerr != nil {
			return "", "", "", "", "", "", "", "", "", rerr
		}
		var person nascPerson
		if uerr := xml.Unmarshal(body, &person); uerr != nil {
			return "", "", "", "", "", "", "", "", "", uerr
		}
		return person.UserID, person.Password, strings.ToLower(person.Email.Address),
			person.Mii.Name, person.Mii.Data, person.Country, person.Language,
			person.TZName, person.Region, nil
	}
	if perr := r.ParseForm(); perr != nil {
		return "", "", "", "", "", "", "", "", "", perr
	}
	return r.FormValue("user_id"), r.FormValue("password"), strings.ToLower(r.FormValue("email.address")),
		r.FormValue("mii.name"), r.FormValue("mii.data"), r.FormValue("country"),
		r.FormValue("language"), r.FormValue("tz_name"), r.FormValue("region"), nil
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
