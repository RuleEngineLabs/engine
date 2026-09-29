package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GlobalOperatorGroup is the Cognito group name that grants full access to all policies.
const GlobalOperatorGroup = "policy-operators"

// ErrUnauthorized is returned when the token is missing or invalid.
var ErrUnauthorized = errors.New("unauthorized")

// ErrForbidden is returned when the token is valid but the caller lacks required group membership.
var ErrForbidden = errors.New("forbidden")

type contextKey struct{}

// Claims holds the parsed JWT claims relevant to authorization.
type Claims struct {
	Groups    []string
	ExpiresAt time.Time
}

// Verifier validates a JWT and extracts its Claims.
type Verifier interface {
	Verify(token string) (*Claims, error)
}

// FromContext retrieves Claims stored by middleware; returns nil if absent.
func FromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(contextKey{}).(*Claims)
	return c
}

// WithClaims stores Claims in the context.
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
}

// IsOwner reports whether the caller belongs to the given owner group or is a global operator.
func IsOwner(ctx context.Context, owner string) bool {
	c := FromContext(ctx)
	if c == nil {
		return false
	}
	for _, g := range c.Groups {
		if g == GlobalOperatorGroup || g == owner {
			return true
		}
	}
	return false
}

// IsOperator reports whether the caller is a global policy-operator.
func IsOperator(ctx context.Context) bool {
	c := FromContext(ctx)
	if c == nil {
		return false
	}
	for _, g := range c.Groups {
		if g == GlobalOperatorGroup {
			return true
		}
	}
	return false
}

// HMACVerifier validates JWTs signed with HMAC-SHA256 using a shared secret.
// Used in tests and non-production environments.
type HMACVerifier struct {
	Secret []byte
}

func (v *HMACVerifier) Verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrUnauthorized
	}

	sigInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, v.Secret)
	mac.Write([]byte(sigInput))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, ErrUnauthorized
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrUnauthorized
	}

	var payload struct {
		CognitoGroups []string `json:"cognito:groups"`
		Exp           int64    `json:"exp"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, ErrUnauthorized
	}

	exp := time.Unix(payload.Exp, 0)
	if payload.Exp > 0 && time.Now().After(exp) {
		return nil, fmt.Errorf("%w: token expired", ErrUnauthorized)
	}

	return &Claims{
		Groups:    payload.CognitoGroups,
		ExpiresAt: exp,
	}, nil
}

// MakeTestToken creates a compact JWT signed with the given HMAC secret for testing.
func MakeTestToken(secret []byte, groups []string, exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

	payload, _ := json.Marshal(map[string]any{
		"cognito:groups": groups,
		"exp":            exp.Unix(),
	})
	payloadEnc := base64.RawURLEncoding.EncodeToString(payload)

	sigInput := header + "." + payloadEnc
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sigInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return sigInput + "." + sig
}
