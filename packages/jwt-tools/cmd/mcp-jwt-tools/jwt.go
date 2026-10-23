package main

// JWT decoding, analysis, verification and test signing (port of src/jwt.ts).

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"errors"
	"fmt"
	"hash"
	"math"
	"regexp"
	"strings"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
)

var algorithms = []string{"HS256", "HS384", "HS512", "RS256", "RS384", "RS512"}

type hashSpec struct {
	newHash func() hash.Hash
	id      crypto.Hash
}

var hashes = map[string]hashSpec{
	"HS256": {sha256.New, crypto.SHA256}, "HS384": {sha512.New384, crypto.SHA384}, "HS512": {sha512.New, crypto.SHA512},
	"RS256": {sha256.New, crypto.SHA256}, "RS384": {sha512.New384, crypto.SHA384}, "RS512": {sha512.New, crypto.SHA512},
}

func isHMAC(algorithm string) bool { return strings.HasPrefix(algorithm, "HS") }

type decoded struct {
	header           *jsv.Obj
	payload          *jsv.Obj
	signaturePresent bool
	signature        []byte
	signingInput     string
}

var (
	bearerPrefix = regexp.MustCompile(`^(?i:bearer)`)
	base64urlRe  = regexp.MustCompile(`^[A-Za-z0-9_-]*$`)
)

// stripBearer is .replace(/^Bearer\s+/i, "").
func stripBearer(s string) string {
	if !bearerPrefix.MatchString(s) || len(s) < 6 {
		return s
	}
	rest := s[6:]
	trimmed := jsv.TrimStart(rest)
	if trimmed == rest {
		return s
	}
	return trimmed
}

func decodeJwt(token string) (*decoded, error) {
	parts := strings.Split(stripBearer(jsv.Trim(token)), ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("a JWS compact token has three dot-separated parts; this one has %d", len(parts))
	}
	h, p, s := parts[0], parts[1], parts[2]
	if !base64urlRe.MatchString(h) || !base64urlRe.MatchString(p) || !base64urlRe.MatchString(s) {
		return nil, errors.New("token parts must be base64url (letters, digits, - and _)")
	}
	header, err := jsv.Parse(decodeUTF8(base64urlDecode(h)))
	if err != nil {
		return nil, errors.New("header is not base64url-encoded JSON")
	}
	payload, err := jsv.Parse(decodeUTF8(base64urlDecode(p)))
	if err != nil {
		return nil, errors.New("payload is not base64url-encoded JSON (encrypted JWE tokens cannot be decoded here)")
	}
	if !jsv.IsObject(header) {
		return nil, errors.New("header is not a JSON object")
	}
	if !jsv.IsObject(payload) {
		return nil, errors.New("payload is not a JSON object")
	}
	return &decoded{header: jsv.AsObj(header), payload: jsv.AsObj(payload), signaturePresent: s != "",
		signature: base64urlDecode(s), signingInput: h + "." + p}, nil
}

// asSeconds returns a claim that is a finite number.
func asSeconds(o *jsv.Obj, key string) (float64, bool) {
	f, ok := jsv.Prop(o, key).(float64)
	return f, ok && !math.IsInf(f, 0) && !math.IsNaN(f)
}

func humanDuration(seconds float64) string {
	abs := math.Abs(seconds)
	switch {
	case abs < 60:
		return jsv.FormatNumber(jsRound(abs)) + " s"
	case abs < 3600:
		return jsv.FormatNumber(jsRound(abs/60)) + " min"
	case abs < 86400:
		return toFixed1(abs/3600) + " h"
	}
	return toFixed1(abs/86400) + " days"
}

type finding struct{ code, severity, message string }

func (f finding) value() *jsv.Obj {
	return jsv.O("code", f.code, "severity", f.severity, "message", f.message)
}

type analysisOptions struct {
	now      float64
	maxTTLH  float64
	hoursSet bool
}

var otherAlgorithm = regexp.MustCompile(`^(ES(256|384|512)|PS(256|384|512)|EdDSA)$`)

// kidSuspicious is /[\/\\'"`;]/.
func kidSuspicious(kid string) bool { return strings.ContainsAny(kid, "/\\'\"`;") }

func analyseJwt(d *decoded, opts analysisOptions) ([]finding, *jsv.Obj, error) {
	now := opts.now
	maxTTL := opts.maxTTLH * 3600
	thresholdText := jsv.FormatNumber(opts.maxTTLH)
	findings := []finding{{"not-verified", "info", "Decoded only. The signature was not checked; nothing in the payload can be trusted until verify_jwt succeeds with the expected key and algorithm."}}
	add := func(code, severity, message string) { findings = append(findings, finding{code, severity, message}) }
	times := jsv.NewObj()
	alg, _ := jsv.Prop(d.header, "alg").(string)
	isNone := alg != "" && asciiLower(alg) == "none"
	switch {
	case alg == "":
		add("alg-missing", "high", "Header has no alg. Libraries may reject it or fall back to an unsafe default.")
	case isNone:
		add("alg-none", "critical", "alg is none: the token is unsigned. Any verifier that accepts none accepts forged tokens.")
	case indexOf(algorithms, alg) < 0 && !otherAlgorithm.MatchString(alg):
		add("alg-unknown", "medium", fmt.Sprintf("alg %s is not a registered JWS algorithm.", jsv.Stringify(alg, 0)))
	}
	if strings.HasPrefix(alg, "HS") {
		add("alg-symmetric", "info", alg+" is symmetric: every party that can verify the token can also mint one.")
	}
	if !d.signaturePresent {
		severity := "critical"
		if isNone {
			severity = "info"
		}
		add("signature-empty", severity, "The signature part is empty.")
	}
	if d.header.Has("jku") || d.header.Has("x5u") {
		add("header-key-url", "high", "Header carries jku or x5u, a URL the verifier may fetch keys from. Verifiers must pin allowed key URLs or ignore these fields.")
	}
	if d.header.Has("jwk") {
		add("header-embedded-jwk", "high", "Header embeds a jwk. A verifier that trusts it accepts a key chosen by the token's author.")
	}
	if kid, ok := jsv.Prop(d.header, "kid").(string); ok && kidSuspicious(kid) {
		add("kid-suspicious", "medium", "kid contains path or quote characters; verifiers that use kid in file paths or SQL are at risk.")
	}
	if typ, ok := d.header.Get("typ"); ok {
		text := jsv.ToString(typ)
		if strings.ToUpper(text) != "JWT" && !strings.Contains(text, "+") {
			add("typ-unusual", "info", fmt.Sprintf("typ is %s rather than JWT.", jsv.Stringify(typ, 0)))
		}
	}

	exp, hasExp := asSeconds(d.payload, "exp")
	iat, hasIat := asSeconds(d.payload, "iat")
	nbf, hasNbf := asSeconds(d.payload, "nbf")
	claims := []struct {
		name  string
		value float64
		has   bool
	}{{"exp", exp, hasExp}, {"iat", iat, hasIat}, {"nbf", nbf, hasNbf}}
	for _, c := range claims {
		switch {
		case c.has && c.value > 1e11:
			add(c.name+"-milliseconds", "medium", fmt.Sprintf("%s=%s looks like milliseconds; JWT timestamps are seconds since the epoch.", c.name, jsv.FormatNumber(c.value)))
		case c.has:
			text, err := isoSeconds(c.value)
			if err != nil {
				return nil, nil, err
			}
			times.Set(c.name, text)
		}
	}
	if !hasExp {
		add("exp-missing", "high", "No exp claim: the token never expires.")
	} else if exp <= 1e11 {
		if exp <= now {
			text, err := isoSeconds(exp)
			if err != nil {
				return nil, nil, err
			}
			add("expired", "high", fmt.Sprintf("Expired %s ago (exp %s).", humanDuration(now-exp), text))
		}
		start, hasStart := iat, hasIat
		if !hasIat {
			start, hasStart = nbf, hasNbf
		}
		if hasStart && start <= 1e11 {
			if ttl := exp - start; ttl > maxTTL {
				severity := "medium"
				if ttl > 365*86400 {
					severity = "high"
				}
				add("long-ttl", severity, fmt.Sprintf("Lifetime is %s (iat/nbf to exp), above the %s h threshold. Long-lived bearer tokens widen the window after a leak.", humanDuration(ttl), thresholdText))
			}
		} else if exp-now > maxTTL {
			add("long-ttl", "medium", fmt.Sprintf("Expires in %s, above the %s h threshold, and there is no iat to measure the full lifetime.", humanDuration(exp-now), thresholdText))
		}
	}
	if hasNbf && nbf <= 1e11 && nbf > now+300 {
		text, err := isoSeconds(nbf)
		if err != nil {
			return nil, nil, err
		}
		add("not-yet-valid", "medium", fmt.Sprintf("nbf is %s in the future (%s).", humanDuration(nbf-now), text))
	}
	if hasIat && iat <= 1e11 && iat > now+300 {
		add("iat-future", "medium", fmt.Sprintf("iat is %s in the future; clocks disagree or the token is forged.", humanDuration(iat-now)))
	}
	if !hasIat {
		add("iat-missing", "low", "No iat claim, so the token's age cannot be measured.")
	}
	if !d.payload.Has("aud") {
		add("aud-missing", "medium", "No aud claim: the token can be replayed against any service that shares the key.")
	}
	if !d.payload.Has("iss") {
		add("iss-missing", "medium", "No iss claim: verifiers cannot pin the issuer.")
	}
	if !d.payload.Has("sub") {
		add("sub-missing", "low", "No sub claim.")
	}
	if !d.payload.Has("jti") {
		add("jti-missing", "info", "No jti claim, so individual tokens cannot be revoked or deduplicated.")
	}
	var sensitive []string
	for _, k := range d.payload.Keys() {
		if isSensitiveClaim(k) {
			sensitive = append(sensitive, k)
		}
	}
	if len(sensitive) > 0 {
		add("sensitive-claims", "high", fmt.Sprintf("Payload contains %s. JWS payloads are only base64url-encoded, not encrypted; anyone holding the token can read them.", strings.Join(sensitive, ", ")))
	}
	return findings, times, nil
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// signingKey is keyObjectFor: the HMAC secret bytes, or a parsed PEM key.
type signingKey struct {
	secret []byte
	key    *keyObject
}

func keyFor(algorithm, key string, sign bool) (signingKey, error) {
	if isHMAC(algorithm) {
		if key == "" {
			return signingKey{}, errors.New("an HMAC key must not be empty")
		}
		return signingKey{secret: encodeUTF8(key)}, nil
	}
	var k *keyObject
	var err error
	what := "public (or private)"
	if sign {
		what = "private"
		k, err = privateKey(key)
	} else {
		k, err = publicKey(key)
	}
	if err != nil {
		return signingKey{}, fmt.Errorf("key is not a PEM %s key: %s", what, err.Error())
	}
	return signingKey{key: k}, nil
}

func hmacSignature(algorithm, input string, secret []byte) []byte {
	mac := hmac.New(hashes[algorithm].newHash, secret)
	mac.Write([]byte(input))
	return mac.Sum(nil)
}

func digest(algorithm, input string) []byte {
	h := hashes[algorithm].newHash()
	h.Write([]byte(input))
	return h.Sum(nil)
}

func computeSignature(algorithm, input string, k signingKey) ([]byte, error) {
	if isHMAC(algorithm) {
		return hmacSignature(algorithm, input, k.secret), nil
	}
	if k.key.kind != "rsa" {
		return nil, fmt.Errorf("%s needs an RSA key, got %s", algorithm, k.key.kind)
	}
	sig, err := rsa.SignPKCS1v15(nil, k.key.private, hashes[algorithm].id, digest(algorithm, input))
	if errors.Is(err, rsa.ErrMessageTooLong) {
		return nil, errors.New(errDigestTooBig)
	}
	return sig, err
}

type verifyOptions struct {
	audience, issuer *string
	now, tolerance   float64
}

func verifyJwt(token, key, algorithm string, opts verifyOptions) (*jsv.Obj, error) {
	d, err := decodeJwt(token)
	if err != nil {
		return nil, err
	}
	result := func(valid, signatureValid, claimsValid bool, reasons []string) *jsv.Obj {
		return jsv.O("valid", valid, "signature_valid", signatureValid, "claims_valid", claimsValid, "algorithm", algorithm,
			"reasons", jsv.Strings(reasons), "payload", d.payload)
	}
	alg, hasAlg := d.header.Get("alg")
	if a, isString := alg.(string); !hasAlg || !isString || a != algorithm {
		shown := "undefined"
		if hasAlg {
			shown = jsv.Stringify(alg, 0)
		}
		return result(false, false, false, []string{fmt.Sprintf("header alg is %s but %s was required; refusing to verify with a different algorithm than expected", shown, algorithm)}), nil
	}
	k, err := keyFor(algorithm, key, false)
	if err != nil {
		return nil, err
	}
	reasons := []string{}
	signatureValid := false
	if isHMAC(algorithm) {
		expected := hmacSignature(algorithm, d.signingInput, k.secret)
		signatureValid = len(expected) == len(d.signature) && subtle.ConstantTimeCompare(expected, d.signature) == 1
	} else if k.key.kind != "rsa" {
		reasons = append(reasons, fmt.Sprintf("%s needs an RSA key, got %s", algorithm, k.key.kind))
	} else {
		signatureValid = rsa.VerifyPKCS1v15(k.key.public, hashes[algorithm].id, digest(algorithm, d.signingInput), d.signature) == nil
	}
	if !signatureValid && len(reasons) == 0 {
		reasons = append(reasons, "signature does not match the supplied key")
	}

	claimsValid := true
	exp, hasExp := asSeconds(d.payload, "exp")
	nbf, hasNbf := asSeconds(d.payload, "nbf")
	if !hasExp {
		claimsValid = false
		reasons = append(reasons, "no exp claim")
	} else if exp+opts.tolerance <= opts.now {
		claimsValid = false
		text, err := isoSeconds(exp)
		if err != nil {
			return nil, err
		}
		reasons = append(reasons, "expired at "+text)
	}
	if hasNbf && nbf-opts.tolerance > opts.now {
		claimsValid = false
		text, err := isoSeconds(nbf)
		if err != nil {
			return nil, err
		}
		reasons = append(reasons, "not valid before "+text)
	}
	if opts.audience != nil {
		aud, hasAud := d.payload.Get("aud")
		var list []any
		switch a := aud.(type) {
		case []any:
			list = a
		default:
			if hasAud {
				list = []any{aud}
			}
		}
		found := false
		for _, x := range list {
			if s, ok := x.(string); ok && s == *opts.audience {
				found = true
			}
		}
		if !found {
			claimsValid = false
			reasons = append(reasons, fmt.Sprintf("aud %s does not include %s", jsv.Stringify(orNull(aud, hasAud), 0), jsv.Stringify(*opts.audience, 0)))
		}
	}
	if opts.issuer != nil {
		iss, hasIss := d.payload.Get("iss")
		if s, ok := iss.(string); !ok || s != *opts.issuer {
			claimsValid = false
			reasons = append(reasons, fmt.Sprintf("iss %s is not %s", jsv.Stringify(orNull(iss, hasIss), 0), jsv.Stringify(*opts.issuer, 0)))
		}
	}
	return result(signatureValid && claimsValid, signatureValid, claimsValid, reasons), nil
}

// orNull is value ?? null.
func orNull(v any, present bool) any {
	if !present {
		return nil
	}
	return v
}

type signOptions struct {
	expiresIn *float64
	now       float64
}

func signJwt(payload *jsv.Obj, key, algorithm string, opts signOptions) (string, error) {
	claims := jsv.O("iat", opts.now)
	for _, k := range payload.Keys() {
		claims.Set(k, jsv.Prop(payload, k))
	}
	if opts.expiresIn != nil {
		claims.Set("exp", opts.now+*opts.expiresIn)
	}
	header := jsv.O("alg", algorithm, "typ", "JWT")
	input := base64urlEncode(encodeUTF8(jsv.Stringify(header, 0))) + "." + base64urlEncode(encodeUTF8(jsv.Stringify(claims, 0)))
	k, err := keyFor(algorithm, key, true)
	if err != nil {
		return "", err
	}
	sig, err := computeSignature(algorithm, input, k)
	if err != nil {
		return "", err
	}
	return input + "." + base64urlEncode(sig), nil
}
