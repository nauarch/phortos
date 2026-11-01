// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package trust is the contract of phortos' certificates: the identity clients pin, the working
// CAs apt trusts for the intercepted hosts, and the certificates phortos presents for them.
package trust

import (
	"crypto/tls"
	"time"
)

// Issuer gives the proxy door what it needs to end TLS for an intercepted host.
type Issuer interface {
	// Intercepts reports whether a host (lower case, no port) is one phortos ends TLS for.
	Intercepts(host string) bool
	// Certificate is the certificate phortos presents for an intercepted host, chained to the
	// current working CA.
	Certificate(host string) (*tls.Certificate, error)
}

// Publisher gives the API door what clients fetch: the bundle, and the identity's certificate
// the API is served with.
type Publisher interface {
	// Bundle is what a client installs.
	Bundle() Bundle
	// Identity is the certificate the API presents; clients pin its key.
	Identity() tls.Certificate
}

// Bundle is what a client installs: the pin it already holds (to compare), the intercepted
// hosts, and the working CAs (current first, then the next one), each signed by the identity.
type Bundle struct {
	Pin   string   `json:"pin"`
	Hosts []string `json:"hosts"`
	CAs   []CA     `json:"cas"`
}

// CA is one working CA: its certificate in PEM, its fingerprint (hex SHA-256 of its DER bytes,
// which clients compare with what they installed), its expiry, and the identity's signature over
// its DER bytes (ECDSA, ASN.1, over SHA-256, base64).
type CA struct {
	PEM         string    `json:"pem"`
	Fingerprint string    `json:"fingerprint"`
	NotAfter    time.Time `json:"not_after"`
	Signature   string    `json:"signature"`
}
