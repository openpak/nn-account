// Package nnas implements the Nintendo Network Account System HTTP surface.
// Port provenance: Pretendo/account f7b1bc2 services/nnas (AGPL-3.0).
package nnas

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"strings"
)

// NintendoPasswordHash ports upstream nintendoPasswordHash: the console-side
// credential transformation sha256(pid_le32 + "\x02\x65\x43\x46" + password).
// This transformed value is what consoles transmit and what the adapter
// forwards to the core's adapter credential domain.
func NintendoPasswordHash(password string, pid int64) string {
	buf := make([]byte, 0, 8+len(password))
	var pidBuf [4]byte
	binary.LittleEndian.PutUint32(pidBuf[:], uint32(pid))
	buf = append(buf, pidBuf[:]...)
	buf = append(buf, 0x02, 0x65, 0x43, 0x46)
	buf = append(buf, password...)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// randomHex returns n bytes of cryptographic randomness hex-encoded (PRD §8).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failure: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// NintendoBase64Encode ports upstream's base64 alphabet swap.
func NintendoBase64Encode(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	s = strings.ReplaceAll(s, "+", ".")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "=", "*")
	return s
}

// CreateServiceToken ports upstream createServiceToken: 28-byte payload
// (pid, titleID, issued, expires) + HMAC-SHA256 under the server AES key.
func CreateServiceToken(aesKey string, pid, titleID uint64, issuedMs, expiresMs int64) []byte {
	data := make([]byte, 28)
	binary.BigEndian.PutUint32(data[0:4], uint32(pid))
	binary.BigEndian.PutUint64(data[4:12], titleID)
	binary.BigEndian.PutUint64(data[12:20], uint64(issuedMs))
	binary.BigEndian.PutUint64(data[20:28], uint64(expiresMs))
	mac := hmac.New(sha256.New, []byte(aesKey))
	mac.Write(data)
	return mac.Sum(nil) // NOTE: upstream appends mac to data; see below.
}

// CreateServiceTokenFull returns the wire token: payload || hmac.
func CreateServiceTokenFull(aesKey string, pid, titleID uint64, issuedMs, expiresMs int64) []byte {
	data := make([]byte, 28)
	binary.BigEndian.PutUint32(data[0:4], uint32(pid))
	binary.BigEndian.PutUint64(data[4:12], titleID)
	binary.BigEndian.PutUint64(data[12:20], uint64(issuedMs))
	binary.BigEndian.PutUint64(data[20:28], uint64(expiresMs))
	mac := hmac.New(sha256.New, []byte(aesKey))
	mac.Write(data)
	return append(data, mac.Sum(nil)...)
}

func writeXMLErr(w http.ResponseWriter, status int, cause, code, message string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xmlError(cause, code, message)))
}

func xmlError(cause, code, message string) string {
	var b strings.Builder
	b.WriteString("<errors><error>")
	if cause != "" {
		b.WriteString("<cause>" + escapeXML(cause) + "</cause>")
	}
	b.WriteString("<code>" + escapeXML(code) + "</code>")
	b.WriteString("<message>" + escapeXML(message) + "</message>")
	b.WriteString("</error></errors>")
	return b.String()
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// nintendoRandomPassword returns an alphanumeric NEX password (upstream
// charset), using cryptographic randomness.
func nintendoRandomPassword(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failure: " + err.Error())
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = charset[int(v)%len(charset)]
	}
	return string(out)
}

// nintendoRandomToken returns base64 of n cryptographically random bytes
// (upstream NEX token shape).
func nintendoRandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failure: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(b)
}
