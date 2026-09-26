// Package nasc implements the Nintendo 3DS NASC service (POST /ac).
// Port provenance: Pretendo/account f7b1bc2 services/nasc + middleware/nasc
// (AGPL-3.0).
package nasc

import (
	"context"
	crand "crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"

	"openpak/nn-account/internal/cert"
	"openpak/nn-account/internal/clientid"
	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/nnas"
	"openpak/nn-account/internal/store"
)

// Server provides what NASC needs from the HTTP layer.
type Server struct {
	pool *pgxpool.Pool
	core *coreclient.Client
}

func New(pool *pgxpool.Pool, core *coreclient.Client) *Server {
	return &Server{pool: pool, core: core}
}

// ServeHTTP handles POST /ac.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.writeError(w, "null")
		return
	}
	p := r.PostForm
	for _, k := range []string{"action", "fcdcert", "csnum", "macadr", "titleid", "servertype", "gameid"} {
		if p.Get(k) == "" {
			s.writeError(w, "null") // Nintendo sends this for malformed input
			return
		}
	}

	action := nintendoDecode(p.Get("action"))
	fcdcert := nintendoDecodeBytes(p.Get("fcdcert"))
	serial := nintendoDecode(p.Get("csnum"))
	macAddress := strings.ToLower(nintendoDecode(p.Get("macadr")))
	titleID := nintendoDecode(p.Get("titleid"))
	environment := nintendoDecode(p.Get("servertype"))
	gameServerID := nintendoDecode(p.Get("gameid"))

	macHash := sha256Base64([]byte(macAddress))
	fcdcertHash := sha256Base64(fcdcert)

	var pid int64
	var password, uidHmac string
	if v := p.Get("userid"); v != "" {
		pid, _ = strconv.ParseInt(nintendoDecode(v), 10, 64)
	}
	if v := p.Get("uidhmac"); v != "" {
		uidHmac = nintendoDecode(v)
	}
	if v := p.Get("passwd"); v != "" {
		password = nintendoDecode(v)
	}

	if action != "LOGIN" && action != "SVCLOC" {
		s.writeError(w, "null")
		return
	}

	// Certificate: structural parse always; crypto verification requires
	// operator keys (see package cert).
	if _, err := cert.Parse(fcdcert); err != nil {
		s.writeError(w, "121")
		return
	}
	if !validNintendoMACAddress(macAddress) {
		s.writeError(w, "null")
		return
	}
	model := modelForSerial(serial)
	if model == "" {
		s.writeError(w, "null")
		return
	}

	ctx := r.Context()

	// Resolve the NEX identity for authenticated requests.
	var nexAccount *store.NEXAccount
	var err error
	ownershipProven := false
	if pid > 0 {
		nexAccount, err = store.GetNEXAccountByPID(ctx, s.pool, pid)
		if err != nil || nexAccount.AccessLevel < 0 {
			// 102: device/account rejected (upstream TODO: account-ban code)
			s.writeError(w, "102")
			return
		}
		// Fail-closed: the owning identity must be active (PRD §7).
		if nexAccount.OwningPID != nil {
			pnid, err := store.GetPNIDByPID(ctx, s.pool, *nexAccount.OwningPID)
			if err != nil || pnid.Deleted {
				s.writeError(w, "102")
				return
			}
			acct, err := s.core.GetAccount(ctx, pnid.AccountID)
			if err != nil || acct.GetStatus() != 1 {
				s.writeError(w, "102")
				return
			}
			// Ownership proof (review P1 #1): a claimed PID is not proof of
			// ownership (FR-2/FR-3). A matching console credential wins;
			// otherwise the device must already be linked to this PID.
			if password != "" {
				vc, verr := s.core.VerifyAdapterCredential(ctx, "wiiu", pnid.AccountID, password)
				if verr != nil || !vc.GetValid() || vc.GetStatus() != 1 {
					s.writeError(w, "102")
					return
				}
				ownershipProven = true
			}
		}
		// Provisional (device-only) identities are claimed with their
		// client-chosen password at registration; a matching passwd proves
		// continued control.
		if nexAccount.OwningPID == nil && password != "" {
			if password != nexAccount.Password {
				s.writeError(w, "102")
				return
			}
			ownershipProven = true
		}
	}

	device, _ := store.GetDeviceByCertHash(ctx, s.pool, fcdcertHash)
	if device != nil {
		if device.AccessLevel < 0 {
			s.writeError(w, "102")
			return
		}
		if pid > 0 && !store.ContainsPID(device.LinkedPIDs, pid) {
			if !ownershipProven {
				// Unknown device/PID pairing without credentials.
				s.writeError(w, "102")
				return
			}
			// System-transfer edge case (upstream): append the PID.
			if err := store.LinkDeviceToPID(ctx, s.pool, fcdcertHash, pid); err != nil {
				s.writeError(w, "null")
				return
			}
		}
		if device.Serial != serial {
			s.writeError(w, "150") // custom upstream code: serial mismatch
			return
		}
	} else if pid > 0 && !ownershipProven {
		// Unknown device claiming a PID with no credentials (review P1 #1).
		s.writeError(w, "102")
		return
	}

	// Device-only registration flow for the account settings title
	// (0004013000003202): creates a provisional NEX identity (FR-2). The
	// record is never treated as a user identity until explicitly claimed.
	if titleID == "0004013000003202" && password != "" && pid == 0 && uidHmac == "" {
		newPID, err := store.GeneratePID(ctx, s.pool)
		if err != nil {
			s.writeError(w, "151")
			return
		}
		friendCode := FriendCodeForPID(newPID)
		if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			if err := store.InsertProvisionalNEXAccount(ctx, tx, newPID, password, "3ds", friendCode); err != nil {
				return err
			}
			if device == nil {
				return store.InsertDevice(ctx, tx, &store.Device{
					FcdcCertHash: fcdcertHash, Model: model, Serial: serial,
					Environment: environment, MacHash: macHash,
					LinkedPIDs: []int64{newPID},
				})
			}
			return store.LinkDeviceToPID(ctx, tx, fcdcertHash, newPID)
		}); err != nil {
			s.writeError(w, "151")
			return
		}
		pid = newPID
		if nexAccount, err = store.GetNEXAccountByPID(ctx, s.pool, pid); err != nil {
			s.writeError(w, "151")
			return
		}
	}

	if nexAccount == nil {
		s.writeError(w, "null")
		return
	}

	// Handler: resolve the server by title and dispatch the action.
	server, err := store.GetServerByTitleID(ctx, s.pool, titleID, nexAccount.ServerAccessLevel)
	if err != nil || server.AESKey == "" {
		s.writeError(w, "110")
		return
	}
	if gameServerID != server.GameServerID {
		// Upstream 152: title needs custom patches (server found by title,
		// but the requested game server ID differs).
		s.writeError(w, "152")
		return
	}
	if server.MaintenanceMode {
		s.writeError(w, "101")
		return
	}
	ip, port := server.ConnectInfo()
	if action == "LOGIN" && port <= 0 && ip != "0.0.0.0" {
		s.writeError(w, "110")
		return
	}

	switch action {
	case "LOGIN":
		s.respondLogin(ctx, w, server, nexAccount, titleID, ip, port, clientid.Of(r, "3ds"), clientid.OSOf(r))
	case "SVCLOC":
		s.respondServiceToken(ctx, w, server, nexAccount, titleID, p.Get("keyhash"))
	}
}

func (s *Server) respondLogin(ctx context.Context, w http.ResponseWriter, server *store.Server, nexAccount *store.NEXAccount, titleID, ip string, port int32, client, os string) {
	token := nintendoEncodeBytes(nintendoRandomBytes(112))
	now := time.Now()
	if err := store.InsertNEXToken(ctx, s.pool, token, server.GameServerID, nexAccount.PID,
		int64(parseHexUint64(titleID)), now, now.Add(time.Hour), client, os); err != nil {
		s.writeError(w, "110")
		return
	}
	writeParams(w, map[string]string{
		"locator":  nintendoEncode(ip + ":" + strconv.Itoa(int(port))),
		"retry":    nintendoEncode("0"),
		"returncd": nintendoEncode("001"),
		"token":    token,
		"datetime": nintendoEncode(nascDateTime()),
	})
}

func (s *Server) respondServiceToken(ctx context.Context, w http.ResponseWriter, server *store.Server, nexAccount *store.NEXAccount, titleID, keyhash string) {
	now := time.Now()
	full := nnas.CreateServiceTokenFull(server.AESKey, uint64(nexAccount.PID), parseHexUint64(titleID),
		now.UnixMilli(), now.Add(24*time.Hour).UnixMilli())
	token := nintendoEncodeBytes(full)
	clientID := nintendoDecode(keyhash)
	if err := store.InsertIndependentServiceToken(ctx, s.pool, token, clientID,
		nexAccount.PID, int64(parseHexUint64(titleID)), now, now.Add(24*time.Hour)); err != nil {
		s.writeError(w, "110")
		return
	}
	writeParams(w, map[string]string{
		"retry":        nintendoEncode("0"),
		"returncd":     nintendoEncode("007"),
		"servicetoken": token,
		"statusdata":   nintendoEncode("Y"),
		"svchost":      nintendoEncode("n/a"),
		"datetime":     nintendoEncode(nascDateTime()),
	})
}

func (s *Server) writeError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	retry := "1"
	returncd := code
	if code == "null" {
		returncd = "null"
	}
	_, _ = w.Write([]byte(fmt.Sprintf("retry=%s&returncd=%s&datetime=%s",
		nintendoEncode(retry), nintendoEncode(returncd), nintendoEncode(nascDateTime()))))
}

func writeParams(w http.ResponseWriter, params map[string]string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	var sb strings.Builder
	for k, v := range params {
		if sb.Len() > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k + "=" + v)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(sb.String()))
}

// ---- helpers ----

func nintendoEncode(s string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	r := strings.NewReplacer("+", ".", "/", "-", "=", "*")
	return r.Replace(enc)
}

func nintendoEncodeBytes(b []byte) string {
	enc := base64.StdEncoding.EncodeToString(b)
	r := strings.NewReplacer("+", ".", "/", "-", "=", "*")
	return r.Replace(enc)
}

func nintendoDecode(s string) string {
	r := strings.NewReplacer(".", "+", "-", "/", "*", "=")
	out, err := base64.StdEncoding.DecodeString(r.Replace(s))
	if err != nil {
		return ""
	}
	return string(out)
}

func nintendoDecodeBytes(s string) []byte {
	r := strings.NewReplacer(".", "+", "-", "/", "*", "=")
	out, err := base64.StdEncoding.DecodeString(r.Replace(s))
	if err != nil {
		return nil
	}
	return out
}

func nintendoRandomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = crand.Read(b)
	return b
}

func sha256Base64(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func nascDateTime() string {
	return time.Now().Format("20060102150405")
}

func parseHexUint64(s string) uint64 {
	v, _ := strconv.ParseUint(s, 16, 64)
	return v
}

func modelForSerial(serial string) string {
	if serial == "" {
		return ""
	}
	switch serial[0] {
	case 'C':
		return "ctr"
	case 'S':
		return "spr"
	case 'A':
		return "ftr"
	case 'Y':
		return "ktr"
	case 'Q':
		return "red"
	case 'N':
		return "jan"
	default:
		return ""
	}
}

// FriendCodeForPID ports the upstream friend-code derivation.
func FriendCodeForPID(pid int64) string {
	buf := make([]byte, 4)
	buf[0] = byte(pid)
	buf[1] = byte(pid >> 8)
	buf[2] = byte(pid >> 16)
	buf[3] = byte(pid >> 24)
	sum := sha1.Sum(buf)
	checksum := int(sum[0]) >> 1
	hex := strconv.FormatInt(int64(checksum), 16) + strconv.FormatInt(pid, 16)
	v, _ := strconv.ParseInt(hex, 16, 64)
	s := fmt.Sprintf("%012d", v)
	if len(s) < 12 {
		s = strings.Repeat("0", 12-len(s)) + s
	}
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12]
}

var macRegex = regexp.MustCompile(`^[0-9a-fA-F]{12}$`)

// nintendoOUIs: https://standards-oui.ieee.org (upstream list).
var nintendoOUIs = map[string]bool{
	"601AC7": true, "BC9EBB": true, "CC5B31": true, "1C4586": true, "E8A0CD": true, "702C09": true,
	"7048F7": true, "98E8FA": true, "ECC40D": true, "606BFF": true, "64B5C6": true, "40D28A": true,
	"A45C27": true, "8C56C5": true, "002659": true, "00241E": true, "002444": true, "98E255": true,
	"E0EFBF": true, "948E6D": true, "38C6CE": true, "C89143": true, "DCCD18": true, "28CF51": true,
	"58B03E": true, "200BCF": true, "748469": true, "70F088": true, "9458CB": true, "582F40": true,
	"B88AEC": true, "A438CC": true, "40F407": true, "A4C0E1": true, "0022D7": true, "001CBE": true,
	"001B7A": true, "001AE9": true, "0009BF": true, "904528": true, "ACFAE4": true, "BC89A6": true,
	"201C3A": true, "7820A5": true, "E0F6B5": true, "342FBD": true, "98415C": true, "D4F057": true,
	"5C521E": true, "98B6E9": true, "CCFB65": true, "B8AE6E": true, "182A7B": true, "2C10C1": true,
	"002331": true, "001E35": true, "001BEA": true, "0017AB": true, "001656": true, "BC744B": true,
	"3CA9AB": true, "C84805": true, "C0A4CF": true, "3089EC": true, "483177": true, "50236D": true,
	"D05509": true, "E8DA20": true, "7CBB8A": true, "34AF2C": true, "78A2A0": true, "E84ECE": true,
	"002709": true, "0025A0": true, "0024F3": true, "0023CC": true, "001F32": true, "001EA9": true,
	"001DBC": true, "0019FD": true, "00191D": true, "A4C1E8": true, "D86B83": true, "4044F7": true,
	"B86870": true, "BCCE25": true, "80D2E5": true, "5C0CE6": true, "74F9CA": true, "48A5E7": true,
	"B87826": true, "DC68EB": true, "0403D6": true, "9CE635": true, "8CCDE8": true, "58BDA3": true,
	"E00C7F": true, "CC9E00": true, "D86BF7": true, "E0E751": true, "0022AA": true, "00224C": true,
	"0021BD": true, "002147": true, "001FC5": true, "48F1EB": true, "78818C": true, "4C306A": true,
}

func validNintendoMACAddress(mac string) bool {
	if len(mac) < 6 {
		return false
	}
	if !nintendoOUIs[strings.ToUpper(mac[:6])] {
		return false
	}
	return macRegex.MatchString(mac)
}
