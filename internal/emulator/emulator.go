// Package emulator is the Wii U/3DS emulator surface, the counterpart of the Switch adapter's
// /internal/switch/emulator: the website asks for the console identity of a signed-in OpenPak
// account and gets a PNID, NEX credentials and (3DS) a friend code, minted on first sight. An
// account that already registered from a real console gets that same identity, so an emulator
// and a console are one person on the network.
package emulator

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	accountv1 "openpak/nn-account/internal/accountpb"
	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/nasc"
	"openpak/nn-account/internal/store"
)

type Server struct {
	pool *pgxpool.Pool
	core *coreclient.Client
	key  string
	mux  *http.ServeMux
}

func New(pool *pgxpool.Pool, core *coreclient.Client, key string) *Server {
	s := &Server{pool: pool, core: core, key: key, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /internal/wiiu/emulator", s.identity("wiiu"))
	s.mux.HandleFunc("GET /internal/3ds/emulator", s.identity("3ds"))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Key")), []byte(s.key)) != 1 {
		http.Error(w, `{"error":"internal"}`, http.StatusUnauthorized)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// Identity is what the emulator writes into its emulated console.
type Identity struct {
	AccountID     string `json:"account_id"`
	PID           int64  `json:"pid"`
	Username      string `json:"username"`
	FriendCode    string `json:"friend_code,omitempty"` // 3DS only
	NEXPassword   string `json:"nex_password"`
	MiiName       string `json:"mii_name"`
	MiiData       string `json:"mii_data"` // base64 FFLStoreData
	DeviceType    string `json:"device_type"`
	PasswordCache string `json:"password_cache,omitempty"` // base64 of the 32 account.dat cache bytes (NA-1a)
	Country       string `json:"country,omitempty"`       // NNAS country name, e.g. "US"
	Language      string `json:"language,omitempty"`       // e.g. "en"
}

func (s *Server) identity(ns string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := r.URL.Query().Get("account_id")
		if accountID == "" {
			http.Error(w, `{"error":"account_id required"}`, http.StatusBadRequest)
			return
		}
		id, err := s.Ensure(r.Context(), ns, accountID)
		switch {
		case errors.Is(err, errNoSuchAccount):
			http.Error(w, `{"error":"unknown account"}`, http.StatusNotFound)
			return
		case err != nil:
			log.Printf("emulator %s: %v", ns, err)
			http.Error(w, `{"error":"identity unavailable"}`, http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(id)
	}
}

var errNoSuchAccount = errors.New("emulator: no such core account")

const defaultMiiData = "AwAAQOlVognnx0GC2/uogAOzuI0n2QAAAEBEAGUAZgBhAHUAbAB0AAAAAAAAAEBAAAAhAQJoRBgmNEYUgRIXaA0AACkAUkhQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGm9"

// Ensure returns the account's identity for the family, creating what is missing: a shadow
// PNID becomes a full one (NEX account, core link), a missing PNID is minted.
func (s *Server) Ensure(ctx context.Context, ns, accountID string) (*Identity, error) {
	acct, err := s.core.GetAccount(ctx, accountID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, errNoSuchAccount
		}
		return nil, err
	}
	switch acct.GetStatus() {
	case accountv1.AccountStatus_ACCOUNT_STATUS_DELETED, accountv1.AccountStatus_ACCOUNT_STATUS_DELETION_PENDING, accountv1.AccountStatus_ACCOUNT_STATUS_BANNED:
		return nil, errNoSuchAccount
	}
	pnid, err := store.GetPNIDByAccountID(ctx, s.pool, accountID)
	if err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if pnid == nil {
		pnid, err = s.mint(ctx, ns, acct)
		if err != nil {
			return nil, err
		}
	}
	nex, err := store.GetNEXAccountByPID(ctx, s.pool, pnid.PID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
		// A shadow PNID (listed on a friend list, never signed in): give it the rest.
		nex, err = s.complete(ctx, ns, pnid)
	}
	if err != nil {
		return nil, err
	}
	cache, err := s.ensurePasswordCache(ctx, ns, pnid)
	if err != nil {
		return nil, err
	}
	id := &Identity{AccountID: accountID, PID: pnid.PID, Username: pnid.Username, NEXPassword: nex.Password,
		MiiName: pnid.MiiName, MiiData: pnid.MiiData, DeviceType: nex.DeviceType, PasswordCache: cache,
		Country: pnid.Country, Language: pnid.Language}
	if ns == "3ds" {
		id.FriendCode = nasc.FriendCodeForPID(pnid.PID)
	}
	return id, nil
}

// ensurePasswordCache returns the account's console credential bytes (base64,
// 32 raw bytes), minting them once when this adapter owns the identity: the
// transformed secret is stored in the core's adapter credential domain, and
// its raw form here, so the NNAS oauth20 grant verifies. A cache minted at
// console registration is already on the row and is returned as-is — an
// emulator and a console are one person on the network, with one credential.
func (s *Server) ensurePasswordCache(ctx context.Context, ns string, pnid *store.PNID) (string, error) {
	if pnid.PasswordCache != "" {
		return base64.StdEncoding.EncodeToString(mustHex(pnid.PasswordCache)), nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	hexCache := hex.EncodeToString(raw)
	if err := s.core.SetAdapterCredential(ctx, ns, pnid.AccountID, hexCache); err != nil {
		return "", err
	}
	if err := store.SetPNIDPasswordCache(ctx, s.pool, pnid.PID, hexCache); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// mustHex decodes a stored cache; rows are only ever written by this package
// from hex.EncodeToString or NintendoPasswordHash, both lowercase hex.
func mustHex(s string) []byte {
	out, err := hex.DecodeString(s)
	if err != nil {
		panic("emulator: stored password cache is not hex: " + err.Error())
	}
	return out
}

func (s *Server) mint(ctx context.Context, ns string, acct *accountv1.GetAccountResponse) (*store.PNID, error) {
	pid, err := store.GeneratePID(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	miiName := acct.GetDisplayName()
	if r := []rune(miiName); len(r) > 10 {
		miiName = string(r[:10])
	}
	if miiName == "" {
		miiName = "OpenPak"
	}
	p := &store.PNID{PID: pid, AccountID: acct.GetAccountId(), ServerAccessLevel: "prod", MiiName: miiName,
		MiiData: defaultMiiData, Country: "US", Language: "en", Region: 1, TimezoneName: "EST5EDT"}
	for _, username := range []string{nnid(acct.GetDisplayName()), "op" + strconv.FormatInt(pid, 10)} {
		if username == "" {
			continue
		}
		p.Username = username
		if err := store.InsertPNID(ctx, s.pool, p); err == nil {
			return p, nil
		} else if !store.IsUniqueViolation(err) {
			return nil, err
		}
	}
	return store.GetPNIDByAccountID(ctx, s.pool, acct.GetAccountId())
}

// complete gives a PNID a NEX account and an active core link for this family.
func (s *Server) complete(ctx context.Context, ns string, pnid *store.PNID) (*store.NEXAccount, error) {
	subject := strconv.FormatInt(pnid.PID, 10)
	if _, err := s.core.GetActiveLink(ctx, ns, subject); err != nil {
		if err := s.core.ReserveAndActivateLink(ctx, ns, subject, pnid.AccountID); err != nil {
			return nil, err
		}
	}
	owning := pnid.PID
	nex := &store.NEXAccount{PID: pnid.PID, OwningPID: &owning, Password: randomPassword(16),
		ServerAccessLevel: "prod", DeviceType: ns}
	if ns == "3ds" {
		nex.FriendCode = nasc.FriendCodeForPID(pnid.PID)
	}
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := store.InsertNEXAccount(ctx, tx, nex); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE pnids SET shadow = false WHERE pid = $1`, pnid.PID)
		return err
	}); err != nil {
		return nil, err
	}
	return nex, nil
}

func randomPassword(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// nnid keeps the NNID alphabet (letters, digits, - _ .) and the 6..16 length NNAS enforces.
func nnid(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	if b.Len() < 6 || b.Len() > 16 {
		return ""
	}
	return b.String()
}
