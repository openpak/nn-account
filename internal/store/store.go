// Package store provides the adapter's PostgreSQL access, migrations, and
// data operations. The adapter never touches the core's tables (PRD §7).
package store

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("store: apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ---- PNIDs ----

type PNID struct {
	PID               int64
	Username          string
	AccountID         string // core account UUID
	AccessLevel       int32
	ServerAccessLevel string
	MiiName           string
	MiiData           string // base64 binary Mii
	MiiHash           string
	Country           string
	Language          string
	Region            int32
	TimezoneName      string
	Deleted           bool
	CreationDate      time.Time
	Shadow            bool   // no console of this family; minted so other surfaces' users can be listed
	PasswordCache     string // hex of the 32 account.dat cache bytes; the NNAS login secret as consoles send it
}

const pnidCols = `pnids.pid, pnids.username, pnids.account_id, pnids.access_level, pnids.server_access_level,
	pnids.mii_name, pnids.mii_data, pnids.mii_hash, pnids.country, pnids.language, pnids.region,
	pnids.timezone_name, pnids.deleted, pnids.creation_date, pnids.shadow, pnids.password_cache`

func scanPNID(row pgx.Row) (*PNID, error) {
	var p PNID
	err := row.Scan(&p.PID, &p.Username, &p.AccountID, &p.AccessLevel, &p.ServerAccessLevel,
		&p.MiiName, &p.MiiData, &p.MiiHash, &p.Country, &p.Language, &p.Region, &p.TimezoneName,
		&p.Deleted, &p.CreationDate, &p.Shadow, &p.PasswordCache)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func GetPNIDByPID(ctx context.Context, q Querier, pid int64) (*PNID, error) {
	return scanPNID(q.QueryRow(ctx, `SELECT `+pnidCols+` FROM pnids WHERE pnids.pid=$1`, pid))
}

func GetPNIDByUsername(ctx context.Context, q Querier, username string) (*PNID, error) {
	return scanPNID(q.QueryRow(ctx, `SELECT `+pnidCols+` FROM pnids WHERE LOWER(pnids.username)=LOWER($1)`, username))
}

func GetPNIDByAccessToken(ctx context.Context, q Querier, rawToken string) (*PNID, error) {
	return scanPNID(q.QueryRow(ctx, `SELECT `+pnidCols+` FROM pnids JOIN oauth_tokens t ON t.pid = pnids.pid
		WHERE t.token_hash = $1 AND t.token_type = 'oauth_access' AND t.expires_at > now()`,
		HashToken(rawToken)))
}

func GetPNIDByBasicAuth(ctx context.Context, q Querier, base64Token string) (*PNID, error) {
	// Basic auth: base64("username:password") — password verified by the core.
	return scanPNID(q.QueryRow(ctx, `SELECT `+pnidCols+` FROM pnids WHERE pnids.pid = (
		SELECT pid FROM basic_auth_cache WHERE token_hash = $1 AND expires_at > now())`, HashToken(base64Token)))
}

// InsertPNID creates a PNID; PID uniqueness is retried by callers.
func InsertPNID(ctx context.Context, q Querier, p *PNID) error {
	_, err := q.Exec(ctx, `INSERT INTO pnids (pid, username, account_id, access_level, server_access_level,
		mii_name, mii_data, mii_hash, country, language, region, timezone_name, shadow, password_cache)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		p.PID, p.Username, p.AccountID, p.AccessLevel, p.ServerAccessLevel,
		p.MiiName, p.MiiData, p.MiiHash, p.Country, p.Language, p.Region, p.TimezoneName, p.Shadow, p.PasswordCache)
	return err
}

// SetPNIDPasswordCache records the console credential bytes (hex) once. The
// first write wins: the cache is minted with the identity, exactly as a
// console writes account.dat at registration, and is never rotated here.
func SetPNIDPasswordCache(ctx context.Context, q Querier, pid int64, hexCache string) error {
	_, err := q.Exec(ctx, `UPDATE pnids SET password_cache=$2 WHERE pid=$1 AND password_cache=''`, pid, hexCache)
	return err
}

func UpdatePNIDProfile(ctx context.Context, q Querier, pid int64, miiName, miiData, country, language, timezone string) error {
	_, err := q.Exec(ctx, `UPDATE pnids SET mii_name=$2, mii_data=$3, country=$4, language=$5, timezone_name=$6,
		updated_at=now() WHERE pid=$1`, pid, miiName, miiData, country, language, timezone)
	return err
}

// ---- NEX accounts ----

type NEXAccount struct {
	PID               int64
	OwningPID         *int64 // nil = device-only provisional record (FR-2)
	Password          string
	AccessLevel       int32
	ServerAccessLevel string
	FriendCode        string
	DeviceType        string
}

func (n *NEXAccount) OwningPIDOrSelf() int64 {
	if n.OwningPID != nil {
		return *n.OwningPID
	}
	return n.PID
}

func scanNEX(row pgx.Row) (*NEXAccount, error) {
	var n NEXAccount
	err := row.Scan(&n.PID, &n.OwningPID, &n.Password, &n.AccessLevel, &n.ServerAccessLevel, &n.FriendCode, &n.DeviceType)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func GetNEXAccountByPID(ctx context.Context, q Querier, pid int64) (*NEXAccount, error) {
	return scanNEX(q.QueryRow(ctx, `SELECT pid, owning_pid, password, access_level, server_access_level, friend_code, device_type
		FROM nex_accounts WHERE pid=$1`, pid))
}

func GetNEXAccountByOwningPID(ctx context.Context, q Querier, pid int64) (*NEXAccount, error) {
	return scanNEX(q.QueryRow(ctx, `SELECT pid, owning_pid, password, access_level, server_access_level, friend_code, device_type
		FROM nex_accounts WHERE owning_pid=$1`, pid))
}

// InsertProvisionalNEXAccount creates a device-only NEX identity (FR-2):
// protocol records only, never treated as a user identity until claimed.
func InsertProvisionalNEXAccount(ctx context.Context, q Querier, pid int64, password, deviceType, friendCode string) error {
	_, err := q.Exec(ctx, `INSERT INTO nex_accounts (pid, owning_pid, password, access_level, server_access_level, friend_code, device_type)
		VALUES ($1, NULL, $2, 0, 'prod', $3, $4)`, pid, password, friendCode, deviceType)
	return err
}

func InsertNEXAccount(ctx context.Context, q Querier, n *NEXAccount) error {
	_, err := q.Exec(ctx, `INSERT INTO nex_accounts (pid, owning_pid, password, access_level, server_access_level, friend_code, device_type)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		n.PID, n.OwningPID, n.Password, n.AccessLevel, n.ServerAccessLevel, n.FriendCode, n.DeviceType)
	return err
}

// UpdateNEXFriendCode sets the friend code on a provisional NEX account.
func UpdateNEXFriendCode(ctx context.Context, q Querier, pid int64, friendCode string) error {
	_, err := q.Exec(ctx, `UPDATE nex_accounts SET friend_code=$2 WHERE pid=$1`, pid, friendCode)
	return err
}

// ---- Servers ----

type Server struct {
	GameServerID    string
	AccessMode      string
	Device          int32 // core SystemType: 1=WUP, 2=CTR
	ClientID        string
	ServiceName     string
	ServiceType     string
	TitleIDs        []string
	IP              string
	IPList          []string
	Port            int32
	AESKey          string
	MaintenanceMode bool
	HealthCheckPort *int32
}

const serverCols = `game_server_id, access_mode, device, client_id, service_name, service_type,
	title_ids, ip, ip_list, port, aes_key, maintenance_mode, health_check_port`

func scanServer(row pgx.Row) (*Server, error) {
	var s Server
	err := row.Scan(&s.GameServerID, &s.AccessMode, &s.Device, &s.ClientID, &s.ServiceName, &s.ServiceType,
		&s.TitleIDs, &s.IP, &s.IPList, &s.Port, &s.AESKey, &s.MaintenanceMode, &s.HealthCheckPort)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ConnectInfo returns one address for the server: a random entry from
// ip_list/ip (upstream getServerConnectInfo; health-check race omitted in the
// first cut and recorded in the M0 inventory as deferred).
func (s *Server) ConnectInfo() (string, int32) {
	ips := make([]string, 0, len(s.IPList)+1)
	ips = append(ips, s.IPList...)
	if s.IP != "" {
		ips = append(ips, s.IP)
	}
	if len(ips) == 0 {
		return "0.0.0.0", 0
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	ip := ips[int(b[0])%len(ips)]
	return ip, s.Port
}

func GetServerByGameServerID(ctx context.Context, q Querier, gameServerID, accessMode string) (*Server, error) {
	return scanServer(q.QueryRow(ctx, `SELECT `+serverCols+` FROM servers
		WHERE game_server_id=$1 AND access_mode=$2`, gameServerID, accessMode))
}

// GetServerByTitleID resolves a server by title with access-mode ordering
// (upstream getServerByTitleID): dev sees dev>test>prod, test sees test>prod.
func GetServerByTitleID(ctx context.Context, q Querier, titleID, accessMode string) (*Server, error) {
	modes := AccessModeOrder(accessMode)
	for _, mode := range modes {
		s, err := scanServer(q.QueryRow(ctx, `SELECT `+serverCols+` FROM servers
			WHERE $1 = ANY(title_ids) AND access_mode=$2`, titleID, mode))
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	return nil, pgx.ErrNoRows
}

// AccessModeOrder ports upstream accessModeOrder.
func AccessModeOrder(mode string) []string {
	switch mode {
	case "dev":
		return []string{"dev", "test", "prod"}
	case "test":
		return []string{"test", "prod"}
	default:
		return []string{"prod"}
	}
}

// ---- Tokens ----

// HashToken derives the storage key for a raw token (SHA-256 hex), matching
// upstream's storage convention.
func HashToken(raw string) string {
	return sha256Hex(raw)
}

func InsertOAuthToken(ctx context.Context, q Querier, rawToken, clientID string, pid int64, tokenType string, titleID int64, issued, expires time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO oauth_tokens (token_hash, client_id, pid, token_type, title_id, issued_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, HashToken(rawToken), clientID, pid, tokenType, titleID, issued, expires)
	return err
}

func InsertNEXToken(ctx context.Context, q Querier, rawToken, gameServerID string, pid, titleID int64, issued, expires time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO nex_tokens (token_hash, game_server_id, pid, title_id, issued_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, HashToken(rawToken), gameServerID, pid, titleID, issued, expires)
	return err
}

func GetNEXToken(ctx context.Context, q Querier, rawToken string) (gameServerID string, pid, titleID int64, issued, expires time.Time, err error) {
	err = q.QueryRow(ctx, `SELECT game_server_id, pid, title_id, issued_at, expires_at FROM nex_tokens
		WHERE token_hash=$1`, HashToken(rawToken)).Scan(&gameServerID, &pid, &titleID, &issued, &expires)
	return
}

func InsertIndependentServiceToken(ctx context.Context, q Querier, rawToken, clientID string, pid, titleID int64, issued, expires time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO independent_service_tokens (token_hash, client_id, title_id, pid, issued_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, HashToken(rawToken), clientID, titleID, pid, issued, expires)
	return err
}

// RevokeTokensForPID drops every token domain for a PID: OAuth,
// independent service (settings applet), and NEX tokens (review P1 #2 —
// deletion revocation previously left service tokens valid).
func RevokeTokensForPID(ctx context.Context, q Querier, pid int64) error {
	for _, stmt := range []string{
		`DELETE FROM oauth_tokens WHERE pid=$1`,
		`DELETE FROM independent_service_tokens WHERE pid=$1`,
		`DELETE FROM nex_tokens WHERE pid=$1`,
	} {
		if _, err := q.Exec(ctx, stmt, pid); err != nil {
			return err
		}
	}
	return nil
}

func MarkEventProcessed(ctx context.Context, q Querier, version int64) error {
	_, err := q.Exec(ctx, `UPDATE processed_events SET version=$1, updated_at=now() WHERE id=1`, version)
	return err
}

func GetProcessedEventVersion(ctx context.Context, q Querier) (int64, error) {
	var v int64
	err := q.QueryRow(ctx, `SELECT version FROM processed_events WHERE id=1`).Scan(&v)
	return v, err
}

func IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}

var ErrNotFound = errors.New("store: not found")

func GetPNIDByRefreshToken(ctx context.Context, q Querier, rawToken string) (*PNID, error) {
	return scanPNID(q.QueryRow(ctx, `SELECT `+pnidCols+` FROM pnids JOIN oauth_tokens t ON t.pid = pnids.pid
		WHERE t.token_hash = $1 AND t.token_type = 'oauth_refresh' AND t.expires_at > now()`,
		HashToken(rawToken)))
}

// LinkDeviceToPID appends pid to the device's linked_pids (upsert device).
func LinkDeviceToPID(ctx context.Context, q Querier, fcdcertHash string, pid int64) error {
	_, err := q.Exec(ctx, `INSERT INTO devices (fcdcert_hash, linked_pids) VALUES ($1, ARRAY[$2::bigint])
		ON CONFLICT (fcdcert_hash) DO UPDATE SET linked_pids = (
			SELECT array_agg(DISTINCT x) FROM unnest(devices.linked_pids || ARRAY[$2::bigint]) x)`,
		fcdcertHash, pid)
	return err
}

// ---- Devices (NASC) ----

type Device struct {
	FcdcCertHash      string
	Model             string
	Serial            string
	Environment       string
	MacHash           string
	AccessLevel       int32
	ServerAccessLevel string
	LinkedPIDs        []int64
}

func GetDeviceByCertHash(ctx context.Context, q Querier, hash string) (*Device, error) {
	var d Device
	err := q.QueryRow(ctx, `SELECT fcdcert_hash, model, serial, environment, mac_hash, access_level, server_access_level, linked_pids
		FROM devices WHERE fcdcert_hash=$1`, hash).
		Scan(&d.FcdcCertHash, &d.Model, &d.Serial, &d.Environment, &d.MacHash, &d.AccessLevel, &d.ServerAccessLevel, &d.LinkedPIDs)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func InsertDevice(ctx context.Context, q Querier, d *Device) error {
	_, err := q.Exec(ctx, `INSERT INTO devices (fcdcert_hash, model, serial, environment, mac_hash, linked_pids)
		VALUES ($1,$2,$3,$4,$5,$6)`, d.FcdcCertHash, d.Model, d.Serial, d.Environment, d.MacHash, d.LinkedPIDs)
	return err
}

func DeviceSerialMatches(d *Device, serial string) bool { return d.Serial == serial }

func ContainsPID(pids []int64, pid int64) bool {
	for _, p := range pids {
		if p == pid {
			return true
		}
	}
	return false
}

// GeneratePID produces a random PID in the console-accepted range
// (upstream convention: 1,000,000,000–1,799,999,999), retrying on collision.
func GeneratePID(ctx context.Context, q Querier) (int64, error) {
	for i := 0; i < 32; i++ {
		pid, err := randomPID()
		if err != nil {
			return 0, err
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pnids WHERE pid=$1)`, pid).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			return pid, nil
		}
	}
	return 0, ErrNotFound
}

func randomPID() (int64, error) {
	b := make([]byte, 4)
	rand.Read(b) // infallible in modern Go; never returns an error

	v := int64(binary.BigEndian.Uint32(b) & 0x7fffffff)
	const min, max = int64(1_000_000_000), int64(1_799_999_999)
	return min + v%(max-min+1), nil
}

// PIDsForAccount returns adapter PIDs for a core account (event handling).
func PIDsForAccount(ctx context.Context, pool *pgxpool.Pool, accountID string) ([]int64, error) {
	q := pool

	rows, err := q.Query(ctx, `SELECT pid FROM pnids WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var pid int64
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		out = append(out, pid)
	}
	return out, rows.Err()
}

// UpdatePNIDMii persists Mii name/data (Nintendo-specific profile asset).
func UpdatePNIDMii(ctx context.Context, q Querier, pid int64, miiName, miiData string) error {
	_, err := q.Exec(ctx, `UPDATE pnids SET mii_name=$2, mii_data=$3, updated_at=now() WHERE pid=$1`,
		pid, miiName, miiData)
	return err
}

func GetIndependentServiceToken(ctx context.Context, q Querier, rawToken string) (clientID string, pid, titleID int64, issued, expires time.Time, err error) {
	err = q.QueryRow(ctx, `SELECT client_id, pid, title_id, issued_at, expires_at FROM independent_service_tokens
		WHERE token_hash=$1`, HashToken(rawToken)).Scan(&clientID, &pid, &titleID, &issued, &expires)
	return
}

// UpdatePNIDSettings applies a partial settings update from the console
// settings applet (adapter-owned profile fields only).
func UpdatePNIDSettings(ctx context.Context, q Querier, pid int64, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	setClauses := make([]string, 0, len(updates))
	args := make([]any, 0, len(updates)+1)
	i := 1
	for col, val := range updates {
		setClauses = append(setClauses, col+"=$"+strconv_itoa(i))
		args = append(args, val)
		i++
	}
	args = append(args, pid)
	_, err := q.Exec(ctx, `UPDATE pnids SET `+strings.Join(setClauses, ", ")+`, updated_at=now() WHERE pid=$`+strconv_itoa(i), args...)
	return err
}

func strconv_itoa(n int) string {
	return strconv.Itoa(n)
}

func GetPNIDByAccountID(ctx context.Context, q Querier, accountID string) (*PNID, error) {
	return scanPNID(q.QueryRow(ctx, `SELECT `+pnidCols+` FROM pnids WHERE pnids.account_id=$1 AND NOT pnids.deleted ORDER BY pnids.shadow, pnids.pid LIMIT 1`, accountID))
}

// GetServerByGameServerIDAnyMode resolves a server regardless of access mode
// (audience existence check on token exchange).
func GetServerByGameServerIDAnyMode(ctx context.Context, q Querier, gameServerID string) (*Server, error) {
	return scanServer(q.QueryRow(ctx, `SELECT `+serverCols+` FROM servers WHERE game_server_id=$1 LIMIT 1`, gameServerID))
}
