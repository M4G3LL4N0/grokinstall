package adversarial

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildTraversalZip creates a zip whose entries escape the destination.
func buildTraversalZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"../../etc/passwd", "/etc/shadow", "ok.txt"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("payload")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
