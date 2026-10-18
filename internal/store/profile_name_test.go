package store

import "testing"

func TestProfileName(t *testing.T) {
	for input, want := range map[string]string{"Hansolo": "Hansolo", "Dino Hansolo": "Dino Hanso", "漢字あいうえおかきくけこ": "漢字あいうえおかきく", "": ""} {
		if got := ProfileName(input); got != want {
			t.Errorf("ProfileName(%q)=%q; want %q", input, got, want)
		}
	}
}
