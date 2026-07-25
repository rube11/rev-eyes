package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestSupabaseVerifierValidatesAuthenticatedAccessToken(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	const keyID = "test-key"

	jwks := marshalJWKS(t, &privateKey.PublicKey, keyID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/v1/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	verifier, err := NewSupabaseVerifier(
		ctx,
		server.URL,
		" beta.tester@example.com ",
	)
	if err != nil {
		cancel()
		t.Fatalf("NewSupabaseVerifier() error = %v", err)
	}
	defer cancel()

	now := time.Now().UTC().Truncate(time.Second)
	validClaims := func() supabaseClaims {
		return supabaseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    server.URL + "/auth/v1",
				Subject:   "user-123",
				Audience:  jwt.ClaimStrings{"authenticated"},
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
			},
			Role:  "authenticated",
			Email: "Beta.Tester@Example.COM",
		}
	}

	accessToken := signToken(t, privateKey, keyID, validClaims())
	userID, err := verifier.Verify(context.Background(), accessToken)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if userID != "user-123" {
		t.Fatalf("Verify() user ID = %q", userID)
	}

	disallowedClaims := validClaims()
	disallowedClaims.Email = "someone-else@example.com"
	if _, err := verifier.Verify(
		context.Background(),
		signToken(t, privateKey, keyID, disallowedClaims),
	); !errors.Is(err, ErrEmailNotAllowed) {
		t.Fatalf("Verify() disallowed email error = %v", err)
	}

	hmacToken := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims())
	hmacToken.Header["kid"] = keyID
	signedHMAC, err := hmacToken.SignedString([]byte("not-a-public-key"))
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	if _, err := verifier.Verify(context.Background(), signedHMAC); err == nil {
		t.Fatal("Verify() accepted HS256 token")
	}

	tests := []struct {
		name   string
		mutate func(*supabaseClaims)
	}{
		{
			name: "wrong issuer",
			mutate: func(claims *supabaseClaims) {
				claims.Issuer = "https://other-project.supabase.co/auth/v1"
			},
		},
		{
			name: "wrong audience",
			mutate: func(claims *supabaseClaims) {
				claims.Audience = jwt.ClaimStrings{"other"}
			},
		},
		{
			name: "expired",
			mutate: func(claims *supabaseClaims) {
				claims.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute))
			},
		},
		{
			name: "missing issued at",
			mutate: func(claims *supabaseClaims) {
				claims.IssuedAt = nil
			},
		},
		{
			name: "wrong role",
			mutate: func(claims *supabaseClaims) {
				claims.Role = "anon"
			},
		},
		{
			name: "missing subject",
			mutate: func(claims *supabaseClaims) {
				claims.Subject = ""
			},
		},
		{
			name: "missing email",
			mutate: func(claims *supabaseClaims) {
				claims.Email = ""
			},
		},
		{
			name: "invalid email",
			mutate: func(claims *supabaseClaims) {
				claims.Email = "not-an-email"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := validClaims()
			test.mutate(&claims)

			if _, err := verifier.Verify(
				context.Background(),
				signToken(t, privateKey, keyID, claims),
			); err == nil {
				t.Fatal("Verify() error = nil")
			}
		})
	}
}

func TestParseAllowedEmails(t *testing.T) {
	t.Parallel()

	allowed, err := parseAllowedEmails(
		" First.Tester@Example.com,second.tester@example.com ",
	)
	if err != nil {
		t.Fatalf("parseAllowedEmails() error = %v", err)
	}
	for _, email := range []string{
		"first.tester@example.com",
		"second.tester@example.com",
	} {
		if _, exists := allowed[email]; !exists {
			t.Fatalf("allowed emails does not contain %q", email)
		}
	}

	for _, value := range []string{
		"",
		"not-an-email",
		"tester@example.com,",
		"Tester <tester@example.com>",
	} {
		if _, err := parseAllowedEmails(value); err == nil {
			t.Fatalf("parseAllowedEmails(%q) error = nil", value)
		}
	}
}

func marshalJWKS(t *testing.T, publicKey *ecdsa.PublicKey, keyID string) []byte {
	t.Helper()

	coordinateSize := (publicKey.Curve.Params().BitSize + 7) / 8
	body, err := json.Marshal(map[string]any{
		"keys": []map[string]string{
			{
				"kty": "EC",
				"use": "sig",
				"alg": jwt.SigningMethodES256.Alg(),
				"kid": keyID,
				"crv": "P-256",
				"x": base64.RawURLEncoding.EncodeToString(
					publicKey.X.FillBytes(make([]byte, coordinateSize)),
				),
				"y": base64.RawURLEncoding.EncodeToString(
					publicKey.Y.FillBytes(make([]byte, coordinateSize)),
				),
			},
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return body
}

func signToken(
	t *testing.T,
	privateKey *ecdsa.PrivateKey,
	keyID string,
	claims supabaseClaims,
) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = keyID
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return signed
}
