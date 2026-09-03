package httpapi

import "testing"

func TestValidateLoopbackAddress(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8097", "localhost:8097", "[::1]:8097"} {
		if err := ValidateLoopbackAddress(addr); err != nil {
			t.Errorf("ValidateLoopbackAddress(%q): %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:8097", "192.168.42.11:8097", "example.com:8097", "127.0.0.1:0", "invalid"} {
		if err := ValidateLoopbackAddress(addr); err == nil {
			t.Errorf("ValidateLoopbackAddress(%q) unexpectedly succeeded", addr)
		}
	}
}
