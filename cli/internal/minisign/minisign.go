// Package minisign verifies minisign signatures. Only the prehashed "ED" form is accepted (the
// default since minisign 0.11): the file is hashed with BLAKE2b-512 and the hash is signed with
// Ed25519; a second signature covers the trusted comment, which names the release.
package minisign

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// PublicKey is a parsed minisign public key.
type PublicKey struct {
	ID  [8]byte
	Key ed25519.PublicKey
}

// IDText is the key ID as minisign prints it: the 8 bytes read as a little-endian number, in hex.
func (k PublicKey) IDText() string {
	return fmt.Sprintf("%016X", binary.LittleEndian.Uint64(k.ID[:]))
}

// ParsePublicKey reads the key from its file text or its bare base64 line.
func ParsePublicKey(text string) (PublicKey, error) {
	var pk PublicKey
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return pk, fmt.Errorf("public key: %w", err)
		}
		if len(raw) != 42 || string(raw[:2]) != "Ed" {
			return pk, errors.New("public key: not an Ed25519 minisign key")
		}
		copy(pk.ID[:], raw[2:10])
		pk.Key = ed25519.PublicKey(bytes.Clone(raw[10:42]))
		return pk, nil
	}
	return pk, errors.New("public key: no key line")
}

type signature struct {
	alg            string
	keyID          [8]byte
	sig            []byte
	trustedComment string
	globalSig      []byte
}

const trustedPrefix = "trusted comment: "

func parseSignature(text string) (signature, error) {
	var s signature
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "untrusted comment:") {
		return s, errors.New("signature: missing untrusted comment line")
	}
	if !strings.HasPrefix(lines[2], trustedPrefix) {
		return s, errors.New("signature: missing trusted comment line")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(raw) != 74 {
		return s, errors.New("signature: wrong length")
	}
	global, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(global) != 64 {
		return s, errors.New("signature: wrong global signature length")
	}
	s.alg = string(raw[:2])
	copy(s.keyID[:], raw[2:10])
	s.sig = raw[10:74]
	s.trustedComment = lines[2][len(trustedPrefix):]
	s.globalSig = global
	return s, nil
}

// Verify returns the trusted comment when sigText is a valid prehashed signature of data by one
// of keys. Every other case is an error: fail closed.
func Verify(keys []string, sigText string, data []byte) (string, error) {
	if len(keys) == 0 {
		return "", errors.New("signature: this build has no release keys")
	}
	s, err := parseSignature(sigText)
	if err != nil {
		return "", err
	}
	if s.alg != "ED" {
		return "", fmt.Errorf("signature: algorithm %q refused, only prehashed ED is accepted", s.alg)
	}
	var pk *PublicKey
	for _, k := range keys {
		p, err := ParsePublicKey(k)
		if err != nil {
			return "", err
		}
		if p.ID == s.keyID {
			pk = &p
			break
		}
	}
	if pk == nil {
		return "", errors.New("signature: made by a key this binary does not trust")
	}
	digest := blake2b.Sum512(data)
	if !ed25519.Verify(pk.Key, digest[:], s.sig) {
		return "", errors.New("signature: file does not match")
	}
	if !ed25519.Verify(pk.Key, append(bytes.Clone(s.sig), []byte(s.trustedComment)...), s.globalSig) {
		return "", errors.New("signature: trusted comment was altered")
	}
	return s.trustedComment, nil
}
