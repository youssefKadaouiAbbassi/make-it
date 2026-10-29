// Package minisigntest makes throwaway minisign keys and signatures for tests and the fake release
// tool. Nothing that ships imports it, so a test key never reaches a default build.
package minisigntest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/blake2b"
)

// Key is a throwaway minisign signing key.
type Key struct {
	ID   [8]byte
	Priv ed25519.PrivateKey
}

// KeyFromSeed derives a key from seed; its ID is the first 8 bytes of the seed's sha256.
func KeyFromSeed(seed [32]byte) Key {
	sum := sha256.Sum256(seed[:])
	k := Key{Priv: ed25519.NewKeyFromSeed(seed[:])}
	copy(k.ID[:], sum[:8])
	return k
}

// NewKey makes a key from a random seed.
func NewKey() Key {
	var seed [32]byte
	_, _ = rand.Read(seed[:]) // never fails: it crashes the program instead
	return KeyFromSeed(seed)
}

// Public is the public key as the base64 line minisign.ParsePublicKey reads.
func (k Key) Public() string {
	raw := append([]byte("Ed"), k.ID[:]...)
	return base64.StdEncoding.EncodeToString(append(raw, k.Priv.Public().(ed25519.PublicKey)...))
}

// Sign returns the .minisig text of a prehashed ("ED") signature of data, whose global signature
// covers the signature and the trusted comment.
func (k Key) Sign(data []byte, trusted string) string {
	digest := blake2b.Sum512(data)
	sig := ed25519.Sign(k.Priv, digest[:])
	global := ed25519.Sign(k.Priv, append(append([]byte{}, sig...), trusted...))
	raw := append(append([]byte("ED"), k.ID[:]...), sig...)
	return fmt.Sprintf("untrusted comment: signature from a throwaway test key\n%s\ntrusted comment: %s\n%s\n",
		base64.StdEncoding.EncodeToString(raw), trusted, base64.StdEncoding.EncodeToString(global))
}
