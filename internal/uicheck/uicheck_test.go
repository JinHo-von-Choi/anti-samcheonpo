package uicheck

import "testing"

func TestOnlyLocalAddressesArePressed(t *testing.T) {
	for address, want := range map[string]bool{"file:///tmp/a.html": true, "http://localhost:3000/": true, "http://127.0.0.1:8080/x": true, "https://example.com/": false, "http://192.168.0.2/": false} {
		if got := Local(address); got != want {
			t.Errorf("%s: %v", address, got)
		}
	}
}
