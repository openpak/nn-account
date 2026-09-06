package nnas

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"openpak/nn-account/internal/config"
	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/store"
)

type Server struct {
	pool  *pgxpool.Pool
	core  *coreclient.Client
	cfg   *config.Config
	mux   *http.ServeMux
	ready bool
}

func New(pool *pgxpool.Pool, core *coreclient.Client, cfg *config.Config) *Server {
	s := &Server{pool: pool, core: core, cfg: cfg, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	// OAuth token generation (console sign-in)
	s.mux.HandleFunc("POST /v1/api/oauth20/access_token/generate", s.handleGenerateAccessToken)
	// Provider tokens
	s.mux.HandleFunc("GET /v1/api/provider/service_token/@me", s.auth(s.handleServiceToken))
	s.mux.HandleFunc("GET /v1/api/provider/nex_token/@me", s.auth(s.handleNEXToken))
	// People
	s.mux.HandleFunc("POST /v1/api/people", s.handleRegisterPerson)
	s.mux.HandleFunc("GET /v1/api/people/{username}", s.handleGetPersonByUsername)
	s.mux.HandleFunc("GET /v1/api/people/@me/profile", s.auth(s.handleProfile))
	s.mux.HandleFunc("GET /v1/api/people/@me/devices", s.auth(s.handleDevices))
	s.mux.HandleFunc("POST /v1/api/people/@me/devices", s.auth(s.handlePostDevices))
	s.mux.HandleFunc("GET /v1/api/people/@me/devices/owner", s.auth(s.handleDevicesOwner))
	s.mux.HandleFunc("GET /v1/api/people/@me/devices/status", s.auth(s.handleDevicesStatus))
	s.mux.HandleFunc("PUT /v1/api/people/@me/devices/@current/inactivate", s.auth(s.handleInactivateDevice))
	s.mux.HandleFunc("PUT /v1/api/people/@me/miis/@primary", s.auth(s.handleSetPrimaryMii))
	s.mux.HandleFunc("POST /v1/api/people/@me/deletion", s.auth(s.handleDeletion))
	s.mux.HandleFunc("GET /v1/api/admin/mapped_ids", s.handleMappedIDs)
	s.mux.HandleFunc("GET /v1/api/content/agreements/{type}/{region}/{version}", s.handleAgreements)
	s.mux.HandleFunc("GET /v1/api/content/time_zones/{country}/{language}", s.handleTimeZones)
	// Support (email confirmation subset)
	s.mux.HandleFunc("GET /v1/api/support/validate/email", s.handleValidateEmail)
	// Admin
	s.mux.HandleFunc("GET /v1/api/admin/time", s.handleTime)
	// Health
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
}

// Middleware

// ctxKey is the context key type for request state.
type ctxKey int

const ctxPNID ctxKey = iota

func (s *Server) auth(next func(http.ResponseWriter, *http.Request, *store.PNID)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := firstHeader(r, "Authorization", "authorization")
		if header == "" {
			writeXMLErr(w, http.StatusUnauthorized, "", "1105", "Email address, username, or password, is not valid")
			return
		}
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 {
			writeXMLErr(w, http.StatusUnauthorized, "", "1105", "Email address, username, or password, is not valid")
			return
		}
		token := strings.TrimSpace(parts[1])
		// Cemu sends the access token hex-encoded (upstream cemu middleware).
		if isCemu(r) && parts[0] == "Bearer" {
			token = hexToBase64(token)
		}
		if parts[0] != "Bearer" {
			// Basic auth on provider routes is not supported here; console
			// device routes that need Basic auth arrive with the devices
			// handlers (M2 continued).
			writeXMLErr(w, http.StatusUnauthorized, "", "1105", "Email address, username, or password, is not valid")
			return
		}
		pnid, err := store.GetPNIDByAccessToken(r.Context(), s.pool, token)
		if err != nil {
			writeXMLErr(w, http.StatusUnauthorized, "access_token", "0005", "Invalid access token")
			return
		}
		if pnid.Deleted {
			writeXMLErr(w, http.StatusBadRequest, "", "0112", pnid.Username)
			return
		}
		// Fail-closed core status check (PRD §7): cached tokens are not
		// permission to bypass a ban.
		acct, err := s.core.GetAccount(r.Context(), pnid.AccountID)
		if err != nil {
			writeXMLErr(w, http.StatusServiceUnavailable, "", "1600", "Unable to process request")
			return
		}
		if acct.GetStatus() != 1 { // ACTIVE
			writeXMLErr(w, http.StatusBadRequest, "", "0108", "Account has been banned")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxPNID, pnid)), pnid)
	}
}

func isCemu(r *http.Request) bool {
	return strings.Contains(r.Header.Get("User-Agent"), "Cemu")
}

func firstHeader(r *http.Request, names ...string) string {
	for _, n := range names {
		if v := r.Header.Get(n); v != "" {
			return v
		}
	}
	return ""
}

func hexToBase64(hexStr string) string {
	const hexDigits = "0123456789abcdefABCDEF"
	ok := len(hexStr)%2 == 0
	if ok {
		for _, c := range hexStr {
			if !strings.ContainsRune(hexDigits, c) {
				ok = false
				break
			}
		}
	}
	if !ok {
		return hexStr
	}
	b := make([]byte, len(hexStr)/2)
	for i := 0; i < len(b); i++ {
		b[i] = hexVal(hexStr[i*2])<<4 | hexVal(hexStr[i*2+1])
	}
	return base64Std.EncodeToString(b)
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

var base64Std = base64.StdEncoding

// handleHealth reports adapter liveness; it does not depend on the core.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","service":"nn-account"}`))
}

// handleTime ports GET /v1/api/admin/time (headers only, empty body).
func (s *Server) handleTime(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Nintendo-Date", strconv.FormatInt(time.Now().UnixMilli(), 10))
	w.Header().Set("Server", "Nintendo 3DS (http)")
	w.WriteHeader(http.StatusOK)
}

// handleValidateEmail ports POST /v1/api/support/validate/email (MX check).
func (s *Server) handleValidateEmail(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	domain := ""
	if at := strings.LastIndex(email, "@"); at >= 0 && at < len(email)-1 {
		domain = email[at+1:]
	}
	if domain == "" {
		writeXMLErr(w, http.StatusOK, "email", "0103", "Email format is invalid")
		return
	}
	records, err := net.LookupMX(domain)
	if err != nil || len(records) == 0 {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(xmlError("", "1126", "The domain \""+domain+"\" is not accessible.")))
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
