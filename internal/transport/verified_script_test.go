package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestPrepareVerifiedScriptPinsVerifiedBytes(t *testing.T) {
	path := t.TempDir() + "/main.py"
	original := []byte("print('original')\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	prepared, err := prepareVerifiedScript(path, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	if err := os.WriteFile(path, []byte("print('replaced')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []byte
	if len(prepared.ExtraFiles) > 0 {
		if _, err := prepared.ExtraFiles[0].Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		got = make([]byte, len(original))
		n, readErr := prepared.ExtraFiles[0].Read(got)
		if readErr != nil {
			t.Fatal(readErr)
		}
		got = got[:n]
	} else {
		got, err = os.ReadFile(prepared.Path)
		if err != nil {
			t.Fatal(err)
		}
	}
	if string(got) != string(original) {
		t.Fatalf("prepared script changed: got %q want %q", got, original)
	}
}

func TestPrepareVerifiedScriptRejectsHashMismatch(t *testing.T) {
	path := t.TempDir() + "/main.py"
	if err := os.WriteFile(path, []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if prepared, err := prepareVerifiedScript(path, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err == nil {
		prepared.close()
		t.Fatal("hash mismatch should fail")
	}
}
