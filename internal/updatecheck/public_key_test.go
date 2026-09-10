package updatecheck

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePublicKeyOpenSSH(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authorized := []byte("ssh-ed25519 " + base64.StdEncoding.EncodeToString(sshWireKey("ssh-ed25519", pub)) + " updater@tcno\n")

	got := NormalizePublicKey(authorized)
	if !bytes.Equal(got, pub) {
		t.Fatalf("NormalizePublicKey = %x, want raw key %x", got, []byte(pub))
	}
}

// authorized_keys shapes OpenSSH accepts but NormalizePublicKey deliberately
// does not: they must pass through untouched rather than yield a wrong key.
func TestNormalizePublicKeyRejectsUnsupportedLines(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := base64.StdEncoding.EncodeToString(sshWireKey("ssh-ed25519", pub))
	for name, line := range map[string]string{
		"comment line":  "# updater key\nssh-ed25519 " + body + "\n",
		"options":       `command="/bin/true",no-pty ssh-ed25519 ` + body + "\n",
		"wrong algo":    "ssh-rsa " + body + "\n",
		"truncated key": "ssh-ed25519 " + base64.StdEncoding.EncodeToString(sshWireKey("ssh-ed25519", pub[:16])) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := NormalizePublicKey([]byte(line)); !bytes.Equal(got, []byte(line)) {
				t.Fatalf("line was parsed as %x, want passthrough", got)
			}
		})
	}
}

func sshWireKey(algo string, key []byte) []byte {
	blob := binary.BigEndian.AppendUint32(nil, uint32(len(algo)))
	blob = append(blob, algo...)
	blob = binary.BigEndian.AppendUint32(blob, uint32(len(key)))
	return append(blob, key...)
}

func TestNormalizePublicKeyPassthrough(t *testing.T) {
	raw := make([]byte, ed25519.PublicKeySize)
	if got := NormalizePublicKey(raw); !bytes.Equal(got, raw) {
		t.Fatalf("raw key changed: %x", got)
	}
	garbage := []byte("not a key")
	if got := NormalizePublicKey(garbage); !bytes.Equal(got, garbage) {
		t.Fatalf("garbage changed: %x", got)
	}
}

// The embedded updater-key.pub must normalize to a raw ed25519 key, or the
// Wails updater rejects every signed release at verify time.
func TestEmbeddedUpdaterKeyNormalizes(t *testing.T) {
	got := NormalizePublicKey(readUpdaterKey(t))
	if len(got) != ed25519.PublicKeySize {
		t.Fatalf("normalized key is %d bytes, want %d; updater cannot use it", len(got), ed25519.PublicKeySize)
	}
}

func TestEmbeddedUpdaterKeyMatchesReleaseSignature(t *testing.T) {
	digest, err := hex.DecodeString("e3ed28b968c4db2bd565b8d5430768e387cad5bd5f7f45fcde719c8bc5f56c3f")
	if err != nil {
		t.Fatal(err)
	}
	sig, err := base64.StdEncoding.DecodeString("o0r8o6VUbVF7mrB1b/iJXJ5KdTlZJ+d3/l77Zj10R9beIFHdS9BOuwmTuWLZfe6D6HjXZOiGtWa/DdWgAgeZAw==")
	if err != nil {
		t.Fatal(err)
	}

	key := NormalizePublicKey(readUpdaterKey(t))
	if len(key) != ed25519.PublicKeySize {
		t.Fatalf("normalized key is %d bytes, want %d", len(key), ed25519.PublicKeySize)
	}
	if !ed25519.Verify(ed25519.PublicKey(key), digest, sig) {
		t.Fatal("updater signing vector does not verify against updater-key.pub; released updates will fail to install")
	}
}

func readUpdaterKey(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "updater-key.pub"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
