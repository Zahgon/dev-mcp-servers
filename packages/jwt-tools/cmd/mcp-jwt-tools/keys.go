package main

// PEM key import as node:crypto (OpenSSL 3) performs it for createPublicKey and
// createPrivateKey with a string key, reduced to what the JWT tools need: an RSA key to
// sign or verify with, or the asymmetricKeyType name of any other key.

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"strings"
)

const (
	errUnsupported  = "error:1E08010C:DECODER routines::unsupported"
	errWrongTag     = "error:068000A8:asn1 encoding routines::wrong tag"
	errInterrupted  = "error:07880109:common libcrypto routines::interrupted or cancelled"
	errDigestTooBig = "error:02000070:rsa routines::digest too big for rsa key"
)

// keyObject is a parsed asymmetric key: kind is its asymmetricKeyType; public and
// private are set for RSA keys only.
type keyObject struct {
	kind    string
	public  *rsa.PublicKey
	private *rsa.PrivateKey
}

var keyTypes = map[string]string{
	"1.2.840.113549.1.1.1":  "rsa",
	"1.2.840.113549.1.1.10": "rsa-pss",
	"1.2.840.10045.2.1":     "ec",
	"1.2.840.10040.4.1":     "dsa",
	"1.2.840.113549.1.3.1":  "dh",
	"1.2.840.10046.2.1":     "dh",
	"1.3.101.110":           "x25519",
	"1.3.101.111":           "x448",
	"1.3.101.112":           "ed25519",
	"1.3.101.113":           "ed448",
}

type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type subjectPublicKeyInfo struct {
	Algorithm algorithmIdentifier
	PublicKey asn1.BitString
}

type privateKeyInfo struct {
	Version    int
	Algorithm  algorithmIdentifier
	PrivateKey []byte
	Rest       asn1.RawValue `asn1:"optional,tag:0"`
}

// pemBlock returns the first PEM block whose type is accepted, as PEM_bytes_read_bio does.
func pemBlock(text string, accept func(string) bool) (*pem.Block, bool) {
	rest := []byte(text)
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			return nil, false
		}
		if accept(block.Type) {
			return block, true
		}
		rest = next
	}
}

func parseSPKI(der []byte) (*keyObject, error) {
	var spki subjectPublicKeyInfo
	if rest, err := asn1.Unmarshal(der, &spki); err != nil || len(rest) != 0 {
		return nil, errors.New(errWrongTag)
	}
	kind, known := keyTypes[spki.Algorithm.Algorithm.String()]
	if !known {
		return nil, errors.New(errUnsupported)
	}
	if kind != "rsa" {
		return &keyObject{kind: kind}, nil
	}
	pub, err := x509.ParsePKCS1PublicKey(spki.PublicKey.RightAlign())
	if err != nil {
		return nil, errors.New(errWrongTag)
	}
	return &keyObject{kind: kind, public: pub}, nil
}

// certificateSPKI extracts the SubjectPublicKeyInfo of an X.509 certificate.
func certificateSPKI(der []byte) ([]byte, error) {
	var cert asn1.RawValue
	if _, err := asn1.Unmarshal(der, &cert); err != nil || !cert.IsCompound {
		return nil, errors.New(errWrongTag)
	}
	var tbs asn1.RawValue
	if _, err := asn1.Unmarshal(cert.Bytes, &tbs); err != nil || !tbs.IsCompound {
		return nil, errors.New(errWrongTag)
	}
	fields := tbs.Bytes
	var items []asn1.RawValue
	for len(fields) > 0 && len(items) < 7 {
		var item asn1.RawValue
		rest, err := asn1.Unmarshal(fields, &item)
		if err != nil {
			return nil, errors.New(errWrongTag)
		}
		items = append(items, item)
		fields = rest
	}
	index := 5
	if len(items) > 0 && items[0].Class == asn1.ClassContextSpecific && items[0].Tag == 0 {
		index = 6
	}
	if len(items) <= index {
		return nil, errors.New(errWrongTag)
	}
	return items[index].FullBytes, nil
}

// publicKey is createPublicKey(text): an SPKI, PKCS#1 or certificate PEM block anywhere
// in the text, in that order of preference, else a private key.
func publicKey(text string) (*keyObject, error) {
	if block, ok := pemBlock(text, func(t string) bool { return t == "PUBLIC KEY" }); ok {
		return parseSPKI(block.Bytes)
	}
	if block, ok := pemBlock(text, func(t string) bool { return t == "RSA PUBLIC KEY" }); ok {
		pub, err := x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return nil, errors.New(errWrongTag)
		}
		return &keyObject{kind: "rsa", public: pub}, nil
	}
	if block, ok := pemBlock(text, func(t string) bool { return t == "CERTIFICATE" || t == "X509 CERTIFICATE" }); ok {
		spki, err := certificateSPKI(block.Bytes)
		if err != nil {
			return nil, err
		}
		return parseSPKI(spki)
	}
	return privateKey(text)
}

func isPrivateKeyType(t string) bool {
	switch t {
	case "PRIVATE KEY", "ENCRYPTED PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "DSA PRIVATE KEY":
		return true
	}
	return false
}

// privateKey is createPrivateKey(text) for an unencrypted private key PEM block.
func privateKey(text string) (*keyObject, error) {
	block, ok := pemBlock(text, isPrivateKeyType)
	if !ok {
		return nil, errors.New(errUnsupported)
	}
	if block.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
		return nil, errors.New(errInterrupted)
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return rsaPrivate(block.Bytes)
	case "EC PRIVATE KEY":
		return &keyObject{kind: "ec"}, nil
	case "DSA PRIVATE KEY":
		return &keyObject{kind: "dsa"}, nil
	}
	var info privateKeyInfo
	if rest, err := asn1.Unmarshal(block.Bytes, &info); err != nil || len(rest) != 0 {
		return nil, errors.New(errUnsupported)
	}
	kind, known := keyTypes[info.Algorithm.Algorithm.String()]
	if !known {
		return nil, errors.New(errUnsupported)
	}
	if kind != "rsa" {
		return &keyObject{kind: kind}, nil
	}
	return rsaPrivate(info.PrivateKey)
}

func rsaPrivate(der []byte) (*keyObject, error) {
	priv, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, errors.New(errUnsupported)
	}
	return &keyObject{kind: "rsa", public: &priv.PublicKey, private: priv}, nil
}
