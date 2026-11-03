package emulator

import "testing"

func TestNNID(t *testing.T) {
	cases := map[string]string{"Alexis Smith": "AlexisSmith", "ab": "", "a really long display name here": "", "x.y_z-123": "x.y_z-123"}
	for in, want := range cases {
		if got := nnid(in); got != want {
			t.Errorf("nnid(%q) = %q, want %q", in, got, want)
		}
	}
	if len(randomPassword(16)) != 16 {
		t.Error("password length")
	}
}

func TestValidateConsolePassword(t *testing.T) {
	cases := []struct {
		pw, id string
		want   error
	}{
		{"goodpass123", "player1", nil},
		{"Sixteen!Chars123", "player1", nil},
		{"short", "player1", ErrPasswordLength},
		{"Seventeen!Chars123", "player1", ErrPasswordLength},
		{"has space1234", "player1", ErrPasswordASCII},
		{"pässword1234", "player1", ErrPasswordASCII},
		{"PlayerOne12", "playerone12", ErrPasswordIsID},
		{"xxPLAYER1xx", "player1", ErrPasswordIsID},
	}
	for _, c := range cases {
		if got := ValidateConsolePassword(c.pw, c.id); got != c.want {
			t.Errorf("ValidateConsolePassword(%q,%q) = %v, want %v", c.pw, c.id, got, c.want)
		}
	}
}
