package emulator

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"openpak/nn-account/internal/nnas"
	"openpak/nn-account/internal/store"
)

// Console password rules, shown to the player on the website. The console only sends what
// the player types, so these are ours: 6 to 16 printable ASCII characters, no spaces, and
// not containing the Network ID.
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

// Modes of pnids.console_pw_mode, and the statuses the endpoint answers.
const (
	ModeSync         = "sync"
	ModeCustom       = "custom"
	ModeIncompatible = "incompatible"
)

// setConsolePassword is POST /internal/wiiu/console-password {account_id, password, mode}.
//
// mode "sync" is the website handing over the OpenPak password it just saw (sign-in, password
// change): the console then accepts that same password. When it cannot be typed on a Wii U the
// credential is not set from it; a separate console password the player chose stays, otherwise
// the credential becomes random so a stale earlier password stops working. mode "custom" (the
// default) is the player's separate console password; a password that breaks the rules is a 422.
//
// Either way the account first gets a full PNID (Ensure), then NintendoPasswordHash(password,
// pid) -- what a real console sends for "Link an existing Network ID" -- goes to the core's
// credential domain and to pnids.password_cache as one value. The plain password is never
// stored or logged.
func (s *Server) setConsolePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID string `json:"account_id"`
		Password  string `json:"password"`
		Mode      string `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || in.AccountID == "" {
		http.Error(w, `{"error":"account_id and password required"}`, http.StatusBadRequest)
		return
	}
	if in.Mode == "" {
		in.Mode = ModeCustom
	}
	if in.Mode != ModeCustom && in.Mode != ModeSync {
		http.Error(w, `{"error":"mode must be sync or custom"}`, http.StatusBadRequest)
		return
	}
	id, err := s.Ensure(r.Context(), "wiiu", in.AccountID)
	switch {
	case errors.Is(err, errNoSuchAccount):
		http.Error(w, `{"error":"unknown account"}`, http.StatusNotFound)
		return
	case err != nil:
		log.Printf("console password: ensure: %v", err)
		http.Error(w, `{"error":"identity unavailable"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fail := func(what string, err error) {
		log.Printf("console password: %s for pid %d: %v", what, id.PID, err)
		http.Error(w, `{"error":"credential unavailable"}`, http.StatusBadGateway)
	}
	answer := func(status, reason string) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "status": status, "reason": reason, "nnid": id.Username, "pid": id.PID})
	}
	verr := ValidateConsolePassword(in.Password, id.Username)
	if verr == nil {
		hash := nnas.NintendoPasswordHash(in.Password, id.PID)
		if err := s.core.SetAdapterCredential(r.Context(), "wiiu", in.AccountID, hash); err != nil {
			fail("core credential", err)
			return
		}
		if err := store.SetConsolePasswordCredential(r.Context(), s.pool, id.PID, hash, in.Mode); err != nil {
			fail("cache", err)
			return
		}
		answer(in.Mode, "")
		return
	}
	if in.Mode == ModeCustom {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": verr.Error()})
		return
	}
	mode, err := store.ConsolePasswordMode(r.Context(), s.pool, id.PID)
	if err != nil {
		fail("mode", err)
		return
	}
	if mode == ModeCustom {
		answer(ModeCustom, verr.Error()) // the separate console password stays
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		fail("random", err)
		return
	}
	hexCache := hex.EncodeToString(raw)
	if err := s.core.SetAdapterCredential(r.Context(), "wiiu", in.AccountID, hexCache); err != nil {
		fail("core credential", err)
		return
	}
	if err := store.SetConsolePasswordCredential(r.Context(), s.pool, id.PID, hexCache, ModeIncompatible); err != nil {
		fail("cache", err)
		return
	}
	answer(ModeIncompatible, verr.Error())
}

// consolePasswordStatus is GET /internal/wiiu/console-password?account_id=: the ID to type on
// the console and where its password comes from. 404 when the account has no usable PNID yet.
func (s *Server) consolePasswordStatus(w http.ResponseWriter, r *http.Request) {
	pnid, err := store.GetPNIDByAccountID(r.Context(), s.pool, r.URL.Query().Get("account_id"))
	if err != nil || pnid.Shadow || pnid.Deleted {
		http.Error(w, `{"error":"no wiiu id"}`, http.StatusNotFound)
		return
	}
	mode, err := store.ConsolePasswordMode(r.Context(), s.pool, pnid.PID)
	if err != nil {
		http.Error(w, `{"error":"unavailable"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"nnid": pnid.Username, "pid": pnid.PID, "mode": mode,
		"min": MinConsolePassword, "max": MaxConsolePassword})
}
