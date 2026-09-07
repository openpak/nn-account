package cert

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// Review P2 #9: short data declaring RSA-4096 must return ErrMalformed,
// never panic on out-of-range signature slices.
func TestParseShortRSA4096(t *testing.T) {
	data := make([]byte, 265)
	binary.BigEndian.PutUint32(data[0:4], 0x10000) // RSA-4096 SHA-1
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on malformed cert: %v", r)
		}
	}()
	if _, err := Parse(data); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed, got %v", err)
	}
}

func TestParseValidStructure(t *testing.T) {
	// Minimal structurally-valid ECDSA/SHA-256 certificate: signature
	// (0x3C) + padding (0x40) keeps the header inside the fixed layout.
	data := new(bytes.Buffer)
	data.Write([]byte{0x00, 0x01, 0x00, 0x05}) // signature type 0x10005
	data.Write(make([]byte, 0x3C))             // signature
	data.Write(make([]byte, 0x40))             // zero padding
	// Pad to 0x108 (issuer/keyType/name/ngKeyID live in the fixed header).
	for data.Len() < 0x108 {
		data.WriteByte(0)
	}
	copy(data.Bytes()[0x80:0x84], []byte("Test")) // issuer prefix
	data.Write(make([]byte, 0x104))               // public key data
	c, err := Parse(data.Bytes())
	if err != nil {
		t.Fatalf("structural parse failed: %v", err)
	}
	if c.SignatureType != 0x10005 {
		t.Fatalf("wrong signature type: %x", c.SignatureType)
	}
	if c.ConsoleType == "" {
		t.Fatal("console type not inferred")
	}
}
