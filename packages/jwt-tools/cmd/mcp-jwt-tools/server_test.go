package main

// The upstream suite test/server.test.mjs, ported case by case. Where the upstream
// cases call signJwt, verifyJwt and decodeJwt directly, the same inputs go through
// sign_test_jwt, verify_jwt and decode_jwt here, so the suite also runs against a server
// started from MCP_SERVER_BIN.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"regexp"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
)

const (
	testNow    = 1_700_000_000
	testSecret = "test-secret-with-enough-length"
)

// rsaPair is a fresh 2048-bit RSA key pair as SPKI and PKCS#8 PEM, like the upstream suite's.
var rsaPair = sync.OnceValues(func() (string, string) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		panic(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
})

type jwtFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
}

type decodedToken struct {
	Verified       bool              `json:"verified"`
	Header         map[string]any    `json:"header"`
	Payload        map[string]any    `json:"payload"`
	SignatureBytes int               `json:"signature_bytes"`
	Times          map[string]string `json:"times"`
	Findings       []jwtFinding      `json:"findings"`
}

type verifyResult struct {
	Valid          bool     `json:"valid"`
	SignatureValid bool     `json:"signature_valid"`
	ClaimsValid    bool     `json:"claims_valid"`
	Reasons        []string `json:"reasons"`
}

type signResult struct {
	Token   string         `json:"token"`
	Header  map[string]any `json:"header"`
	Payload map[string]any `json:"payload"`
}

func codes(r decodedToken) []string {
	out := make([]string, len(r.Findings))
	for i, f := range r.Findings {
		out[i] = f.Code
	}
	return out
}

func encodeJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(v): %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (c *mcpClient) sign(args map[string]any) signResult {
	c.t.Helper()
	return decodeResult[signResult](c.t, c.call("sign_test_jwt", args))
}

func (c *mcpClient) verify(args map[string]any) verifyResult {
	c.t.Helper()
	return decodeResult[verifyResult](c.t, c.call("verify_jwt", args))
}

func (c *mcpClient) decode(args map[string]any) decodedToken {
	c.t.Helper()
	return decodeResult[decodedToken](c.t, c.call("decode_jwt", args))
}

func TestListsTheThreeTools(t *testing.T) {
	c := startServer(t)
	init := c.initialize("2025-06-18")
	var result struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(init.Result, &result); err != nil || result.ServerInfo.Name != serverName {
		t.Fatalf("serverInfo.name: %s", init.raw)
	}
	reply := c.request("tools/list", map[string]any{})
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations struct {
				OpenWorldHint *bool `json:"openWorldHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &list); err != nil {
		t.Fatalf("tools/list: %s", reply.raw)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Errorf("%s: openWorldHint is not false", tool.Name)
		}
	}
	sort.Strings(names)
	if want := []string{"decode_jwt", "sign_test_jwt", "verify_jwt"}; !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
}

func TestDecodeJwtNeverVerifiesAndFlagsTheDocumentedProblems(t *testing.T) {
	c := connected(t)
	good := c.sign(map[string]any{"payload": map[string]any{"sub": "u1", "aud": "api", "iss": "https://issuer.example", "jti": "1", "exp": testNow + 600},
		"key": testSecret, "algorithm": "HS256", "now": testNow}).Token
	r := c.decode(map[string]any{"token": "Bearer " + good, "now": testNow})
	if r.Verified || r.Header["alg"] != "HS256" || r.Payload["sub"] != "u1" {
		t.Errorf("good token: %+v", r)
	}
	if want := time.Unix(testNow+600, 0).UTC().Format("2006-01-02T15:04:05.000Z"); r.Times["exp"] != want {
		t.Errorf("times.exp %q, want %q", r.Times["exp"], want)
	}
	if want := []string{"not-verified", "alg-symmetric"}; !slices.Equal(codes(r), want) {
		t.Errorf("codes %v, want %v", codes(r), want)
	}

	noneToken := encodeJSON(t, map[string]any{"alg": "none"}) + "." +
		encodeJSON(t, map[string]any{"sub": "x", "password": "hunter2", "exp": testNow - 10, "iat": testNow - 40*86400}) + "."
	bad := c.decode(map[string]any{"token": noneToken, "now": testNow})
	for _, want := range []string{"alg-none", "signature-empty", "expired", "long-ttl", "aud-missing", "iss-missing", "sensitive-claims"} {
		if !slices.Contains(codes(bad), want) {
			t.Errorf("unsigned token lacks %s: %v", want, codes(bad))
		}
	}
	for _, f := range bad.Findings {
		if f.Code == "alg-none" && f.Severity != "critical" {
			t.Errorf("alg-none severity %s", f.Severity)
		}
	}

	header := encodeJSON(t, map[string]any{"alg": "RS256", "jku": "https://attacker.example/keys", "jwk": map[string]any{"kty": "RSA"}})
	weird := c.decode(map[string]any{"token": header + "." + encodeJSON(t, map[string]any{"exp": testNow * 1000, "nbf": testNow + 7200}) + ".AAAA", "now": testNow})
	for _, want := range []string{"header-key-url", "header-embedded-jwk", "exp-milliseconds", "not-yet-valid", "iat-missing"} {
		if !slices.Contains(codes(weird), want) {
			t.Errorf("header token lacks %s: %v", want, codes(weird))
		}
	}

	for _, token := range []string{"a.b", "a.b.c.d.e", "e30.bm90anNvbg.sig"} {
		if r := c.call("decode_jwt", map[string]any{"token": token}); !r.IsError {
			t.Errorf("%s decoded: %s", token, r.Text)
		}
	}
}

func TestVerifyJwtChecksHS256AndRS256SignaturesClaimsAndRefusesAlgorithmConfusion(t *testing.T) {
	pub, priv := rsaPair()
	c := connected(t)
	hs := c.sign(map[string]any{"payload": map[string]any{"sub": "u1", "aud": []string{"api", "web"}, "iss": "me"}, "key": testSecret, "algorithm": "HS256", "now": testNow, "expires_in_seconds": 300}).Token
	ok := c.verify(map[string]any{"token": hs, "key": testSecret, "algorithm": "HS256", "audience": "api", "issuer": "me", "now": testNow + 10})
	if !ok.Valid || len(ok.Reasons) != 0 {
		t.Errorf("valid HS256: %+v", ok)
	}
	if wrongKey := c.verify(map[string]any{"token": hs, "key": "other", "algorithm": "HS256", "now": testNow}); wrongKey.SignatureValid || wrongKey.Valid {
		t.Errorf("wrong key: %+v", wrongKey)
	}
	expired := c.verify(map[string]any{"token": hs, "key": testSecret, "algorithm": "HS256", "now": testNow + 301})
	if !expired.SignatureValid || expired.ClaimsValid || len(expired.Reasons) == 0 || !regexp.MustCompile("expired").MatchString(expired.Reasons[0]) {
		t.Errorf("expired: %+v", expired)
	}
	if tolerated := c.verify(map[string]any{"token": hs, "key": testSecret, "algorithm": "HS256", "now": testNow + 301, "clock_tolerance_seconds": 5}); !tolerated.Valid {
		t.Errorf("tolerated: %+v", tolerated)
	}
	if wrongAud := c.verify(map[string]any{"token": hs, "key": testSecret, "algorithm": "HS256", "audience": "admin", "issuer": "you", "now": testNow}); len(wrongAud.Reasons) != 2 {
		t.Errorf("wrong audience and issuer: %+v", wrongAud)
	}

	rs := c.sign(map[string]any{"payload": map[string]any{"sub": "u2"}, "key": priv, "algorithm": "RS256", "now": testNow, "expires_in_seconds": 60}).Token
	if rsOK := c.verify(map[string]any{"token": rs, "key": pub, "algorithm": "RS256", "now": testNow}); !rsOK.Valid {
		t.Errorf("RS256 with the public key: %+v", rsOK)
	}
	if rsPriv := c.verify(map[string]any{"token": rs, "key": priv, "algorithm": "RS256", "now": testNow}); !rsPriv.SignatureValid {
		t.Errorf("RS256 with the private key: %+v", rsPriv)
	}
	tampered := rs[:len(rs)-4] + "AAAA"
	if rsBad := c.verify(map[string]any{"token": tampered, "key": pub, "algorithm": "RS256", "now": testNow}); rsBad.SignatureValid {
		t.Errorf("tampered RS256: %+v", rsBad)
	}

	// Algorithm confusion: an HS256 token signed with the public key text must not verify when RS256 is expected.
	confused := c.sign(map[string]any{"payload": map[string]any{"sub": "u3"}, "key": pub, "algorithm": "HS256", "now": testNow, "expires_in_seconds": 60}).Token
	refused := c.verify(map[string]any{"token": confused, "key": pub, "algorithm": "RS256", "now": testNow})
	if refused.Valid || len(refused.Reasons) == 0 || !regexp.MustCompile(`header alg is "HS256" but RS256 was required`).MatchString(refused.Reasons[0]) {
		t.Errorf("algorithm confusion: %+v", refused)
	}
	if badKey := c.call("verify_jwt", map[string]any{"token": rs, "key": "not a pem", "algorithm": "RS256"}); !badKey.IsError {
		t.Errorf("a key that is not PEM was accepted: %s", badKey.Text)
	}
	noExpToken := c.sign(map[string]any{"payload": map[string]any{"sub": "x"}, "key": testSecret, "algorithm": "HS256", "now": testNow}).Token
	noExp := c.verify(map[string]any{"token": noExpToken, "key": testSecret, "algorithm": "HS256", "now": testNow})
	if noExp.ClaimsValid || !slices.Equal(noExp.Reasons, []string{"no exp claim"}) {
		t.Errorf("no exp: %+v", noExp)
	}
}

func TestSignTestJwtProducesTokensThatDecodeAndVerify(t *testing.T) {
	pub, priv := rsaPair()
	c := connected(t)
	r := c.sign(map[string]any{"payload": map[string]any{"sub": "t", "aud": "api"}, "key": testSecret, "algorithm": "HS512", "expires_in_seconds": 120, "now": testNow})
	if r.Header["alg"] != "HS512" || r.Payload["iat"] != float64(testNow) || r.Payload["exp"] != float64(testNow+120) {
		t.Errorf("HS512 token: %+v", r)
	}
	if v := c.verify(map[string]any{"token": r.Token, "key": testSecret, "algorithm": "HS512", "now": testNow}); !v.Valid {
		t.Errorf("HS512 token does not verify: %+v", v)
	}
	rs := c.sign(map[string]any{"payload": map[string]any{"sub": "t"}, "key": priv, "algorithm": "RS384", "expires_in_seconds": 10, "now": testNow})
	if v := c.verify(map[string]any{"token": rs.Token, "key": pub, "algorithm": "RS384", "now": testNow}); !v.Valid {
		t.Errorf("RS384 token does not verify: %+v", v)
	}
	if noKey := c.call("sign_test_jwt", map[string]any{"payload": map[string]any{}, "key": pub, "algorithm": "RS256"}); !noKey.IsError {
		t.Errorf("signing with a public key succeeded: %s", noKey.Text)
	}
	if d := c.decode(map[string]any{"token": r.Token, "now": testNow}); d.SignatureBytes != 64 {
		t.Errorf("HS512 signature has %d bytes", d.SignatureBytes)
	}
}
