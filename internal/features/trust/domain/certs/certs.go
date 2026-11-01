// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package certs makes phortos' certificates: the identity (an ECDSA P-256 key, pinned by the
// SHA-256 of its public key, as curl's --pinnedpubkey spells it), working CAs name-constrained to
// the intercepted hosts and signed by the identity, and short-lived leaf certificates for each
// host. ECDSA rather than Ed25519, which older TLS stacks lack.
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/seal"
)

// Backdate is how far before now a certificate starts, so a client whose clock runs behind still
// accepts it.
const Backdate = 3 * time.Hour

// IdentityName is the name on the identity's own certificate; clients check the pin, not names.
const IdentityName = "phortos.internal"

// NewKey is a fresh ECDSA P-256 key.
func NewKey() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }

// Pin is how clients name a public key (see seal.Pin).
func Pin(pub *ecdsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}
	return seal.Pin(der)
}

// IdentityCertificate is the self-signed certificate phortos' API presents with the identity
// key, valid for lifetime from now.
func IdentityCertificate(key *ecdsa.PrivateKey, now time.Time, lifetime time.Duration) (tls.Certificate, error) {
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: IdentityName},
		DNSNames:     []string{IdentityName},
		NotBefore:    now.Add(-Backdate),
		NotAfter:     now.Add(lifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("identity certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// Authority is a working CA: its certificate and key.
type Authority struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

// NewAuthority is a working CA valid from now for lifetime, allowed to vouch only for hosts
// (each with its subdomains, as X.509 name constraints work).
func NewAuthority(hosts []string, now time.Time, lifetime time.Duration) (Authority, error) {
	if len(hosts) == 0 {
		return Authority{}, fmt.Errorf("working CA: no hosts to vouch for: %w", errkind.ErrInvalid)
	}
	key, err := NewKey()
	if err != nil {
		return Authority{}, err
	}
	sn := serial()
	tmpl := &x509.Certificate{
		SerialNumber:                sn,
		Subject:                     pkix.Name{CommonName: "phortos working CA " + sn.Text(16)},
		NotBefore:                   now.Add(-Backdate),
		NotAfter:                    now.Add(lifetime),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         sorted(hosts),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return Authority{}, fmt.Errorf("working CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return Authority{}, fmt.Errorf("working CA: %w", err)
	}
	return Authority{Cert: cert, Key: key}, nil
}

// Hosts are the hosts a working CA may vouch for, sorted.
func (a Authority) Hosts() []string { return sorted(a.Cert.PermittedDNSDomains) }

// Leaf is the certificate phortos presents for host, issued by the working CA from now for
// lifetime, never past the CA's own end.
func (a Authority) Leaf(host string, now time.Time, lifetime time.Duration) (*tls.Certificate, error) {
	key, err := NewKey()
	if err != nil {
		return nil, err
	}
	end := now.Add(lifetime)
	if end.After(a.Cert.NotAfter) {
		end = a.Cert.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    now.Add(-Backdate),
		NotAfter:     end,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.Cert, &key.PublicKey, a.Key)
	if err != nil {
		return nil, fmt.Errorf("certificate for %s: %w", host, err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("certificate for %s: %w", host, err)
	}
	return &tls.Certificate{Certificate: [][]byte{der, a.Cert.Raw}, PrivateKey: key, Leaf: leaf}, nil
}

// EncodeKey is a private key in PEM.
func EncodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// DecodeKey reads a private key from PEM.
func DecodeKey(b []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("not an EC private key: %w", errkind.ErrInvalid)
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errkind.ErrInvalid, err)
	}
	return key, nil
}

// EncodeCert is a certificate in PEM.
func EncodeCert(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}

// EncodeAuthority is a working CA's certificate and key in one PEM file.
func EncodeAuthority(a Authority) ([]byte, error) {
	key, err := EncodeKey(a.Key)
	if err != nil {
		return nil, err
	}
	return append(EncodeCert(a.Cert), key...), nil
}

// DecodeAuthority reads a working CA's certificate and key from one PEM file, checking that they
// belong together.
func DecodeAuthority(b []byte) (Authority, error) {
	var a Authority
	for {
		block, rest := pem.Decode(b)
		if block == nil {
			break
		}
		b = rest
		switch block.Type {
		case "CERTIFICATE":
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return Authority{}, fmt.Errorf("working CA: %w: %w", errkind.ErrInvalid, err)
			}
			a.Cert = c
		case "EC PRIVATE KEY":
			k, err := x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return Authority{}, fmt.Errorf("working CA key: %w: %w", errkind.ErrInvalid, err)
			}
			a.Key = k
		}
	}
	if a.Cert == nil || a.Key == nil {
		return Authority{}, fmt.Errorf("working CA file lacks its certificate or key: %w", errkind.ErrInvalid)
	}
	pub, ok := a.Cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&a.Key.PublicKey) {
		return Authority{}, errors.Join(errkind.ErrInvalid, errors.New("working CA certificate and key do not match"))
	}
	return a, nil
}

// serial is a random certificate serial number.
func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

// sorted is hosts lower-cased, deduplicated and sorted.
func sorted(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, strings.ToLower(h))
	}
	slices.Sort(out)
	return slices.Compact(out)
}
