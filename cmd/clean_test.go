package cmd

import "testing"

func TestCleanEnabled(t *testing.T) {
	for _, value := range []string{"1", "TRUE", " Yes ", "true"} {
		if !cleanEnabled(value) {
			t.Errorf("cleanEnabled(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "0", "false", "no", "on"} {
		if cleanEnabled(value) {
			t.Errorf("cleanEnabled(%q) = true, want false", value)
		}
	}
}
