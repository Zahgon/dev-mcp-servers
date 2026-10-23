// Command mcp-jwt-tools is the jwt-tools MCP server on stdio: it decodes, analyses,
// verifies and signs JSON Web Tokens.
package main

import (
	"fmt"
	"math"
	"time"

	"github.com/basitalisandhu/dev-mcp-servers/internal/jsv"
	"github.com/basitalisandhu/dev-mcp-servers/internal/mcp"
)

const (
	serverName    = "jwt-tools"
	serverVersion = "0.1.0"
	maxToken      = 16384
	maxKey        = 16384
)

const instructions = "Inspects JSON Web Tokens locally. decode_jwt shows the header and claims without verifying anything and lists risk findings (alg none, expired, missing exp, aud or iss, long lifetimes, key URLs in the header, sensitive claims). " +
	"verify_jwt checks the signature with a key you supply for one expected algorithm (HS256/384/512 with a shared secret, RS256/384/512 with a PEM public key) and then the time, audience and issuer claims. " +
	"sign_test_jwt mints a token for test fixtures. Nothing leaves the process."

func newServer() *mcp.Server {
	return &mcp.Server{Name: serverName, Version: serverVersion, Instructions: instructions, Tools: tools()}
}

func main() { mcp.Main(newServer()) }

// nowSeconds is Math.floor(Date.now() / 1000).
func nowSeconds() float64 { return math.Floor(float64(time.Now().UnixMilli()) / 1000) }

func orNow(now *float64) float64 {
	if now != nil {
		return *now
	}
	return nowSeconds()
}

func decodeTool(args *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "decode_jwt", Obj: args}
	token, err := a.RequiredString("token", 3, maxToken)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	now, err := a.Number("now", 0, 1e11, true)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	hours, err := a.Number("max_ttl_hours", 0.01, 8760*10, false)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	opts := analysisOptions{now: orNow(now), maxTTLH: 24}
	if hours != nil {
		opts.maxTTLH = *hours
	}
	d, err := decodeJwt(token)
	if err != nil {
		return mcp.Fail("Cannot decode token: " + err.Error())
	}
	findings, times, err := analyseJwt(d, opts)
	if err != nil {
		return mcp.Fail("Cannot decode token: " + err.Error())
	}
	list := make([]any, len(findings))
	for i, f := range findings {
		list[i] = f.value()
	}
	return mcp.OK(jsv.O("verified", false, "header", d.header, "payload", d.payload, "signature_present", d.signaturePresent,
		"signature_bytes", float64(len(d.signature)), "times", times, "findings", list))
}

func algorithmArg(a mcp.Args) (string, error) {
	alg, err := a.Enum("algorithm", algorithms)
	if err != nil {
		return "", err
	}
	if alg == nil {
		return "", mcp.Invalid(a.Tool, "algorithm: Required")
	}
	return *alg, nil
}

func verifyTool(args *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "verify_jwt", Obj: args}
	token, err := a.RequiredString("token", 3, maxToken)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	key, err := a.RequiredString("key", 1, maxKey)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	algorithm, err := algorithmArg(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	audience, err := a.String("audience", 0, 500)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	issuer, err := a.String("issuer", 0, 500)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	now, err := a.Number("now", 0, 1e11, true)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	tolerance, err := a.Number("clock_tolerance_seconds", 0, 3600, true)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	opts := verifyOptions{audience: audience, issuer: issuer, now: orNow(now)}
	if tolerance != nil {
		opts.tolerance = *tolerance
	}
	out, err := verifyJwt(token, key, algorithm, opts)
	if err != nil {
		return mcp.Fail("Cannot verify token: " + err.Error())
	}
	return mcp.OK(out)
}

// payloadArg is z.record(z.string().max(200), z.unknown()): an object whose keys are at
// most 200 UTF-16 code units; zod leaves a "__proto__" key out of the parsed record.
func payloadArg(a mcp.Args) (*jsv.Obj, error) {
	raw, present := a.Obj.Get("payload")
	if !present {
		return nil, mcp.Invalid(a.Tool, "payload: Required")
	}
	obj := jsv.AsObj(raw)
	if obj == nil {
		return nil, mcp.Invalid(a.Tool, "payload: Expected object")
	}
	out := jsv.NewObj()
	for _, k := range obj.Keys() {
		if jsv.UTF16Len(k) > 200 {
			return nil, mcp.Invalid(a.Tool, "payload: String must contain at most 200 character(s)")
		}
		if k != "__proto__" {
			out.Set(k, jsv.Prop(obj, k))
		}
	}
	return out, nil
}

func signTool(args *jsv.Obj) mcp.Result {
	a := mcp.Args{Tool: "sign_test_jwt", Obj: args}
	payload, err := payloadArg(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	key, err := a.RequiredString("key", 1, maxKey)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	algorithm, err := algorithmArg(a)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	expiresIn, err := a.Number("expires_in_seconds", 1, 10*365*86400, true)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	now, err := a.Number("now", 0, 1e11, true)
	if err != nil {
		return mcp.Fail(err.Error())
	}
	if jsv.UTF16Len(jsv.Stringify(payload, 0)) > maxToken {
		return mcp.Fail(fmt.Sprintf("payload exceeds %d bytes", maxToken))
	}
	token, err := signJwt(payload, key, algorithm, signOptions{expiresIn: expiresIn, now: orNow(now)})
	if err != nil {
		return mcp.Fail("Cannot sign: " + err.Error())
	}
	d, err := decodeJwt(token)
	if err != nil {
		return mcp.Fail("Cannot sign: " + err.Error())
	}
	return mcp.OK(jsv.O("token", token, "header", d.header, "payload", d.payload))
}

func annotations(idempotent bool) *jsv.Obj {
	return jsv.O("readOnlyHint", true, "destructiveHint", false, "idempotentHint", idempotent, "openWorldHint", false)
}

func schema(props *jsv.Obj, required ...string) *jsv.Obj {
	return jsv.O("type", "object", "properties", props, "required", jsv.Strings(required), "additionalProperties", false,
		"$schema", "http://json-schema.org/draft-07/schema#")
}

func describedString(min, max float64, description string) *jsv.Obj {
	o := jsv.O("type", "string")
	if min > 0 {
		o.Set("minLength", min)
	}
	o.Set("maxLength", max)
	if description != "" {
		o.Set("description", description)
	}
	return o
}

func integer(min, max float64, description string) *jsv.Obj {
	o := jsv.O("type", "integer", "minimum", min, "maximum", max)
	if description != "" {
		o.Set("description", description)
	}
	return o
}

func algorithmSchema(description string) *jsv.Obj {
	o := jsv.O("type", "string", "enum", jsv.Strings(algorithms))
	if description != "" {
		o.Set("description", description)
	}
	return o
}

func tools() []mcp.Tool {
	token := func() *jsv.Obj {
		return describedString(3, maxToken, "Compact JWS token (header.payload.signature). A leading 'Bearer ' is ignored.")
	}
	tool := func(name string, handler func(*jsv.Obj) mcp.Result, title, description string, input *jsv.Obj, idempotent bool) mcp.Tool {
		spec := jsv.O("title", title, "description", description, "inputSchema", input, "annotations", annotations(idempotent),
			"execution", jsv.O("taskSupport", "forbidden"))
		return mcp.Tool{Name: name, Spec: spec, Handler: handler}
	}
	return []mcp.Tool{
		tool("decode_jwt", decodeTool, "Decode a JWT without verifying it",
			"Base64url-decode the header and payload of a JWS compact token and report findings. The signature is NOT verified; the output says so and the claims must be "+
				"treated as unverified. Findings cover: alg none or missing, empty signature, jku/x5u/jwk in the header, expired, not yet valid, missing exp/iat/aud/iss/sub/jti, "+
				"lifetime above max_ttl_hours (default 24), timestamps given in milliseconds, and claims whose names suggest secrets. Timestamps are also returned as ISO 8601. "+
				"JWE (encrypted, five-part) tokens are rejected.",
			schema(jsv.O("token", token(),
				"now", integer(0, 1e11, "Unix seconds to evaluate exp and nbf against. Default: current time."),
				"max_ttl_hours", jsv.O("type", "number", "minimum", 0.01, "maximum", float64(87600), "description", "Lifetime threshold for the long-ttl finding, in hours. Default 24.")),
				"token"), true),
		tool("verify_jwt", verifyTool, "Verify a JWT signature and claims",
			"Verify the signature of a JWS compact token with the supplied key for exactly the given algorithm, then check exp (required), nbf, and optionally aud and iss. "+
				"HS256/HS384/HS512 take the shared secret as key; RS256/RS384/RS512 take a PEM public key, certificate or private key. A token whose header alg differs from "+
				"algorithm fails without any cryptographic check, which blocks algorithm-confusion attacks. Returns valid (both signature and claims), signature_valid, claims_valid and the reasons.",
			schema(jsv.O("token", token(),
				"key", describedString(1, maxKey, "Shared secret (HS*) or PEM-encoded key (RS*)."),
				"algorithm", algorithmSchema("The one algorithm the verifier accepts."),
				"audience", describedString(0, 500, "Required aud value (matches a string aud or one element of an array aud)."),
				"issuer", describedString(0, 500, "Required iss value."),
				"now", integer(0, 1e11, "Unix seconds for time checks. Default: current time."),
				"clock_tolerance_seconds", integer(0, 3600, "Leeway for exp and nbf. Default 0.")),
				"token", "key", "algorithm"), true),
		tool("sign_test_jwt", signTool, "Sign a JWT for test fixtures",
			"Create a signed JWS compact token from a claims object for use in tests and local development. iat is set to now unless the payload supplies one; exp is set from "+
				"expires_in_seconds when given. HS* algorithms use key as the shared secret; RS* algorithms need a PEM private key. Returns the token and the final header and claims.",
			schema(jsv.O("payload", jsv.O("type", "object", "additionalProperties", jsv.O(), "propertyNames", jsv.O("maxLength", float64(200)),
				"description", "Claims to sign, for example {\"sub\":\"user-1\",\"aud\":\"api\"}."),
				"key", describedString(1, maxKey, ""),
				"algorithm", algorithmSchema(""),
				"expires_in_seconds", integer(1, 10*365*86400, ""),
				"now", integer(0, 1e11, "Unix seconds used for iat and exp. Default: current time.")),
				"payload", "key", "algorithm"), false),
	}
}
