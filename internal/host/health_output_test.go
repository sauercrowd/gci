package host

import "testing"

func TestCappedHealthWriterAcceptsAndCapsOutput(t *testing.T) {
	w := &cappedHealthWriter{limit: 5}
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("first Write() = %d, %v", n, err)
	}
	if n, err := w.Write([]byte("defghi")); n != 6 || err != nil {
		t.Fatalf("second Write() = %d, %v", n, err)
	}
	if got := w.String(); got != "abcde" {
		t.Fatalf("captured output = %q, want abcde", got)
	}
}
