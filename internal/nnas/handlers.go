package nnas

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	accountv1 "openpak/nn-account/internal/accountpb"
	"openpak/nn-account/internal/clientid"
	"openpak/nn-account/internal/store"
)

// handleGenerateAccessToken ports POST /v1/api/oauth20/access_token/generate.
// Password grants delegate to the core's adapter credential domain; the
// adapter never verifies console passwords itself (PRD FR-1/§7).
func (s *Server) handleGenerateAccessToken(w http.ResponseWriter, r *http.Request) {
	grantType := r.FormValue("grant_type")
	userID := r.FormValue("user_id")
	password := r.FormValue("password")
	refreshToken := r.FormValue("refresh_token")

	if grantType != "password" && grantType != "refresh_token" {
		writeXMLErr(w, http.StatusBadRequest, "grant_type", "0004", "Invalid Grant Type")
		return
	}

	var pnid *store.PNID
	var err error

	switch grantType {
	case "password":
		if strings.TrimSpace(userID) == "" {
			writeXMLErr(w, http.StatusBadRequest, "user_id", "0002", "user_id format is invalid")
			return
		}
		if strings.TrimSpace(password) == "" {
			writeXMLErr(w, http.StatusBadRequest, "password", "0002", "password format is invalid")
			return
		}
		pnid, err = store.GetPNIDByUsername(r.Context(), s.pool, userID)
		if err != nil {
			writeXMLErr(w, http.StatusBadRequest, "", "0106", "Invalid account ID or password")
			return
		}
		// Forward the console-transformed secret to the core (protected by
		// the internal API key; never persisted or logged here).
		vc, err := s.core.VerifyAdapterCredential(r.Context(), "wiiu", pnid.AccountID, password)
		if err != nil {
			// Fail closed on unreachable core or any internal failure (PRD §7).
			writeXMLErr(w, http.StatusBadRequest, "", "0106", "Invalid account ID or password")
			return
		}
		// Status first: banned/deleted accounts must get protocol-correct errors.
		if vc.GetStatus() == accountv1.AccountStatus_ACCOUNT_STATUS_BANNED {
			writeXMLErr(w, http.StatusBadRequest, "", "0108", "Account has been banned")
			return
		}
		if vc.GetStatus() == accountv1.AccountStatus_ACCOUNT_STATUS_DELETED ||
			vc.GetStatus() == accountv1.AccountStatus_ACCOUNT_STATUS_DELETION_PENDING {
			writeXMLErr(w, http.StatusBadRequest, "", "0112", pnid.Username)
			return
		}
		if !vc.GetValid() {
			writeXMLErr(w, http.StatusBadRequest, "", "0106", "Invalid account ID or password")
			return
		}
		// Active-link enforcement (review P1 #3): an unlinked console may
		// not sign in even while its master account is healthy.
		if _, err := s.core.GetActiveLink(r.Context(), "wiiu", strconv.FormatInt(pnid.PID, 10)); err != nil {
			writeXMLErr(w, http.StatusBadRequest, "", "0106", "Invalid account ID or password")
			return
		}
	case "refresh_token":
		if strings.TrimSpace(refreshToken) == "" {
			writeXMLErr(w, http.StatusBadRequest, "refresh_token", "0106", "Invalid Refresh Token")
			return
		}
		pnid, err = store.GetPNIDByRefreshToken(r.Context(), s.pool, refreshToken)
		if err != nil {
			writeXMLErr(w, http.StatusBadRequest, "refresh_token", "0106", "Invalid Refresh Token")
			return
		}
		// Refresh grants must also revalidate core status AND the active
		// link (FR-7; review P1 #3).
		acct, err := s.core.GetAccount(r.Context(), pnid.AccountID)
		if err != nil || acct.GetStatus() != accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE {
			writeXMLErr(w, http.StatusBadRequest, "", "0108", "Account has been banned")
			return
		}
		if _, err := s.core.GetActiveLink(r.Context(), "wiiu", strconv.FormatInt(pnid.PID, 10)); err != nil {
			writeXMLErr(w, http.StatusBadRequest, "", "0106", "Invalid Refresh Token")
			return
		}
	}

	if pnid.Deleted {
		writeXMLErr(w, http.StatusBadRequest, "", "0112", pnid.Username)
		return
	}

	// Device linkage: attach this console to the PNID's linked devices when
	// a device certificate identity is present. (Certificate chain
	// validation is deferred; see docs/M0-INVENTORY.md.)
	s.linkDeviceOptional(r, pnid)

	clientID := r.Header.Get("X-Nintendo-Client-ID")
	accessToken := randomHex(16)
	newRefreshToken := randomHex(20)
	now := time.Now()

	if err := store.InsertOAuthToken(r.Context(), s.pool, accessToken, clientID, pnid.PID,
		"oauth_access", 0, now, now.Add(time.Hour)); err != nil {
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}
	if err := store.InsertOAuthToken(r.Context(), s.pool, newRefreshToken, clientID, pnid.PID,
		"oauth_refresh", 0, now, now.Add(12*time.Hour)); err != nil {
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = w.Write([]byte(`<OAuth20><access_token><token>` + accessToken +
		`</token><refresh_token>` + newRefreshToken +
		`</refresh_token><expires_in>3600</expires_in></access_token></OAuth20>`))
}

// linkDeviceOptional records the device link when cert headers are present.
func (s *Server) linkDeviceOptional(r *http.Request, pnid *store.PNID) {
	deviceID := r.Header.Get("X-Nintendo-Device-Cert")
	if deviceID == "" {
		return
	}
	_ = store.LinkDeviceToPID(r.Context(), s.pool, deviceID, pnid.PID)
}

// handleServiceToken ports GET /v1/api/provider/service_token/@me.
func (s *Server) handleServiceToken(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	clientID := r.URL.Query().Get("client_id")
	titleID := r.Header.Get("X-Nintendo-Title-ID")
	server := s.resolveServer(r, w, clientID, "", titleID, pnid)
	if server == nil {
		return
	}
	now := time.Now()
	token := CreateServiceTokenFull(server.AESKey, uint64(pnid.PID), parseTitleID(titleID),
		now.UnixMilli(), now.Add(24*time.Hour).UnixMilli())
	_ = server.Device // system type recorded on the token row via title context
	encoded := NintendoBase64Encode(token)
	if err := store.InsertIndependentServiceToken(r.Context(), s.pool, encoded, clientID,
		pnid.PID, int64(parseTitleID(titleID)), now, now.Add(24*time.Hour)); err != nil {
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = w.Write([]byte(`<service_token><token>` + encoded + `</token></service_token>`))
}

// handleNEXToken ports GET /v1/api/provider/nex_token/@me.
func (s *Server) handleNEXToken(w http.ResponseWriter, r *http.Request, pnid *store.PNID) {
	nexAccount, err := store.GetNEXAccountByOwningPID(r.Context(), s.pool, pnid.PID)
	if err != nil {
		writeXMLErr(w, http.StatusNotFound, "", "0008", "Not Found")
		return
	}
	gameServerID := r.URL.Query().Get("game_server_id")
	titleID := r.Header.Get("X-Nintendo-Title-ID")
	server := s.resolveServer(r, w, "", gameServerID, titleID, pnid)
	if server == nil {
		return
	}
	token := nintendoRandomToken(36) // base64 of 36 random bytes (upstream shape)
	now := time.Now()
	if err := store.InsertNEXToken(r.Context(), s.pool, token, gameServerID, pnid.PID,
		int64(parseTitleID(titleID)), now, now.Add(time.Hour), clientid.Of(r, "wiiu"), clientid.OSOf(r)); err != nil {
		writeXMLErr(w, http.StatusInternalServerError, "", "1600", "Unable to process request")
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = w.Write([]byte(`<nex_token><host>` + escapeXML(server.IP) +
		`</host><nex_password>` + escapeXML(nexAccount.Password) +
		`</nex_password><pid>` + strconv.FormatInt(nexAccount.PID, 10) +
		`</pid><port>` + strconv.FormatInt(int64(server.Port), 10) +
		`</port><token>` + escapeXML(token) + `</token></nex_token>`))
}

// resolveServer centralizes the not-found/maintenance checks shared by the
// provider routes (upstream error semantics preserved).
func (s *Server) resolveServer(r *http.Request, w http.ResponseWriter, clientID, gameServerID, titleID string, pnid *store.PNID) *store.Server {
	if (clientID == "" && gameServerID == "") || titleID == "" {
		writeXMLErr(w, http.StatusOK, "", "1021", "The requested game server was not found")
		return nil
	}
	var server *store.Server
	var err error
	if gameServerID != "" {
		server, err = store.GetServerByGameServerID(r.Context(), s.pool, gameServerID, pnid.ServerAccessLevel)
		if err != nil {
			server, err = store.GetServerByGameServerID(r.Context(), s.pool, gameServerID, "prod")
		}
	} else {
		server, err = store.GetServerByClientIDFallback(r.Context(), s.pool, clientID, pnid.ServerAccessLevel)
	}
	if err != nil || server.AESKey == "" {
		writeXMLErr(w, http.StatusOK, "", "1021", "The requested game server was not found")
		return nil
	}
	if server.MaintenanceMode {
		writeXMLErr(w, http.StatusOK, "", "2002", "The requested game server is under maintenance")
		return nil
	}
	return server
}

func parseTitleID(s string) uint64 {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0
	}
	return v
}
