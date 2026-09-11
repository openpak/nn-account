package emulator

import "testing"

func TestNNID(t *testing.T) {
	cases := map[string]string{"Thiago Avila": "ThiagoAvila", "ab": "", "a really long display name here": "", "x.y_z-123": "x.y_z-123"}
	for in, want := range cases {
		if got := nnid(in); got != want {
			t.Errorf("nnid(%q) = %q, want %q", in, got, want)
		}
	}
	if len(randomPassword(16)) != 16 {
		t.Error("password length")
	}
}
