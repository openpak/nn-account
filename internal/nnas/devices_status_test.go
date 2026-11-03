package nnas

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// A real Wii U asks this while linking an existing NNID; a 404 is error 102-2534.
func TestDeviceStatusAnswersEmptyDevice(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).handleDeviceStatus(w, httptest.NewRequest("GET", "/v1/api/devices/@current/status", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<device></device>") {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}
