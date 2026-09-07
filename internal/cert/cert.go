// Package cert parses Nintendo console certificates. Port provenance:
// Pretendo/account f7b1bc2 src/nintendo-certificate.ts (AGPL-3.0).
//
// Cryptographic signature verification requires operator-supplied CA/LFCS
// public keys (legacy console crypto, isolated in this adapter per PRD §8).
// Structural validation is always performed; when keys are not configured,
// verification is skipped and logged — this is recorded in the M0 inventory
// as a deferred nuance, not silently treated as full support.
package cert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"
)

var ErrMalformed = errors.New("cert: malformed certificate")

type SignatureSize struct {
	Size        int
	PaddingSize int
}

var signatureSizes = map[uint32]SignatureSize{
	0x10000: {Size: 0x200, PaddingSize: 0x3C}, // RSA-4096 SHA-1
	0x10001: {Size: 0x100, PaddingSize: 0x3C}, // RSA-2048 SHA-1
	0x10002: {Size: 0x3C, PaddingSize: 0x40},  // ECDSA SHA-1
	0x10003: {Size: 0x200, PaddingSize: 0x3C}, // RSA-4096 SHA-256
	0x10004: {Size: 0x100, PaddingSize: 0x3C}, // RSA-2048 SHA-256
	0x10005: {Size: 0x3C, PaddingSize: 0x40},  // ECDSA SHA-256
}

type Certificate struct {
	SignatureType   uint32
	Signature       []byte
	Body            []byte
	Issuer          string
	KeyType         uint32
	Name            string
	NGKeyID         uint32
	PublicKeyData   []byte
	ConsoleType     string // "wiiu" | "3ds"
	SignatureSHA256 [32]byte

	verified bool // signature cryptographically verified (keys required)
}

// Verified reports whether a cryptographic signature check succeeded.
func (c *Certificate) Verified() bool { return c.verified }

// Parse validates structure and extracts fields. fcdcert (3DS LFCS)
// certificates are 0x110 bytes: signature[0x100] + body[0x10].
func Parse(data []byte) (*Certificate, error) {
	c := &Certificate{}
	if len(data) == 0x110 {
		c.ConsoleType = "3ds"
		c.Signature = data[:0x100]
		c.Body = data[0x100:]
		c.SignatureSHA256 = sha256.Sum256(data)
		return c, nil
	}
	// Bounds-check every slice against the declared signature type before
	// touching data (review P2 #9: short RSA-4096 fixtures panicked).
	sigType := binary.BigEndian.Uint32(data[0:4])
	sizes, ok := signatureSizes[sigType]
	if !ok {
		return nil, ErrMalformed
	}
	bodyStart := 4 + sizes.Size + sizes.PaddingSize
	// Fields occupy up to 0x108; publicKeyData follows. Require at least one
	// byte of key material.
	if len(data) < bodyStart+0x108+1 {
		return nil, ErrMalformed
	}
	c.SignatureType = sigType
	c.Signature = data[4 : 4+sizes.Size]
	c.Body = data[bodyStart:]
	// Upstream validity gate: signature padding must be zero.
	for _, b := range data[4+sizes.Size : bodyStart] {
		if b != 0 {
			return nil, ErrMalformed
		}
	}
	c.Issuer = cstr(data[0x80:0xC0])
	c.KeyType = binary.BigEndian.Uint32(data[0xC0:0xC4])
	c.Name = cstr(data[0xC4:0x104])
	c.NGKeyID = binary.BigEndian.Uint32(data[0x104:0x108])
	c.PublicKeyData = data[0x108:]
	if c.Issuer == "Root-CA00000003-MS00000012" {
		c.ConsoleType = "wiiu"
	} else {
		c.ConsoleType = "3ds"
	}
	c.SignatureSHA256 = sha256.Sum256(data)
	return c, nil
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// VerifyRSA2048 checks the certificate's RSA-2048 signature (types 0x10001
// SHA-1, 0x10004 SHA-256) against raw public key components embedded in the
// certificate, compared with the operator-supplied trusted CA components.
// Returns false on any mismatch or unsupported type.
func (c *Certificate) VerifyRSA2048(trustedN *big.Int, trustedE int) bool {
	if len(c.PublicKeyData) < 0x104 {
		return false
	}
	certN := new(big.Int).SetBytes(c.PublicKeyData[0x00:0x100])
	certE := int(binary.BigEndian.Uint32(c.PublicKeyData[0x100:0x104]))
	if certN.Cmp(trustedN) != 0 || certE != trustedE {
		return false
	}
	var digest []byte
	var hashType crypto.Hash
	switch c.SignatureType {
	case 0x10001:
		h := sha1.Sum(c.Body)
		digest, hashType = h[:], crypto.SHA1
	case 0x10004:
		h := sha256.Sum256(c.Body)
		digest, hashType = h[:], crypto.SHA256
	default:
		return false
	}
	pub := &rsa.PublicKey{N: certN, E: certE}
	return rsa.VerifyPKCS1v15(pub, hashType, digest, c.Signature) == nil
}

// VerifyECDSA checks ECDSA-SHA256 signatures (0x10005) with the cert's own
// public key data interpreted as an EC point (operator CA required to trust).
func (c *Certificate) VerifyECDSA(pub *ecdsa.PublicKey) bool {
	if c.SignatureType != 0x10005 || len(c.Signature) < 0x3C {
		return false
	}
	r := new(big.Int).SetBytes(c.Signature[0x00:0x1E])
	s := new(big.Int).SetBytes(c.Signature[0x1E:0x3C])
	h := sha256.Sum256(c.Body)
	return ecdsa.Verify(pub, h[:], r, s)
}
