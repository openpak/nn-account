package main

import "testing"

func TestUnlinkedConsolePIDIgnoresOtherNetworks(t *testing.T) {
	for _, subject := range []string{"0009d941ad372efd", "1756632087"} {
		for _, namespace := range []string{"xbox", "x360", "switch", "np", ""} {
			if _, relevant, err := unlinkedConsolePID(namespace, subject); relevant || err != nil {
				t.Fatalf("unrelated unlink %s/%s blocks or revokes Nintendo identity", namespace, subject)
			}
		}
	}
	for _, namespace := range []string{"wiiu", "3ds"} {
		pid, relevant, err := unlinkedConsolePID(namespace, "1756632087")
		if err != nil || !relevant || pid != 1756632087 {
			t.Fatalf("Nintendo unlink not handled: %s", namespace)
		}
		if _, relevant, err := unlinkedConsolePID(namespace, "malformed"); !relevant || err == nil {
			t.Fatal("malformed Nintendo event must retain retry behavior")
		}
	}
}
