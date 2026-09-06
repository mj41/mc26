package user

import (
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"io"
	"time"

	pk "github.com/mj41/go-mc26/net/packet"
)

type PublicKey struct {
	ExpiresAt time.Time
	PubKey    *rsa.PublicKey
	Signature []byte
}

func (p PublicKey) WriteTo(w io.Writer) (n int64, err error) {
	pubKeyEncoded, err := x509.MarshalPKIXPublicKey(p.PubKey)
	if err != nil {
		return 0, err
	}
	return pk.Tuple{
		pk.Long(p.ExpiresAt.UnixMilli()),
		pk.ByteArray(pubKeyEncoded),
		pk.ByteArray(p.Signature),
	}.WriteTo(w)
}

func (p *PublicKey) ReadFrom(r io.Reader) (n int64, err error) {
	var (
		ExpiresAt pk.Long
		PubKey    pk.ByteArray
		Signature pk.ByteArray
	)
	n, err = pk.Tuple{
		&ExpiresAt,
		&PubKey,
		&Signature,
	}.ReadFrom(r)
	if err != nil {
		return n, err
	}
	*p, err = ParsePublicKey(int64(ExpiresAt), PubKey, Signature)
	return n, err
}

// ParsePublicKey builds a PublicKey from its wire parts: the expiry in
// milliseconds, the PKIX-encoded RSA key and Mojang's signature over it.
func ParsePublicKey(expiresAtMillis int64, keyDER, signature []byte) (PublicKey, error) {
	pubKey, err := x509.ParsePKIXPublicKey(keyDER)
	if err != nil {
		return PublicKey{}, err
	}
	key, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return PublicKey{}, errors.New("expect RSA public key")
	}
	return PublicKey{ExpiresAt: time.UnixMilli(expiresAtMillis), PubKey: key, Signature: signature}, nil
}

func (p *PublicKey) Verify() bool {
	if p.ExpiresAt.Before(time.Now()) {
		return false
	}
	encoded, err := x509.MarshalPKIXPublicKey(p.PubKey)
	if err != nil {
		return false
	}
	return VerifySignature(encoded, p.Signature)
}

func (p *PublicKey) VerifyMessage(hash, signature []byte) error {
	return rsa.VerifyPKCS1v15(p.PubKey, crypto.SHA256, hash, signature)
}
