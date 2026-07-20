package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

const (
	jwksRefreshInterval = 10 * time.Minute
	jwksRequestTimeout  = 5 * time.Second
	tokenClockSkew      = 30 * time.Second
)

var (
	ErrInvalidAccessToken  = errors.New("invalid access token")
	ErrSupabaseURLRequired = errors.New("SUPABASE_URL is required")
)

// TokenVerifier returns the authenticated Supabase user ID from an access token.
type TokenVerifier func(ctx context.Context, accessToken string) (string, error)

// SupabaseVerifier verifies access tokens with the project's public signing keys.
type SupabaseVerifier struct {
	issuer string
	keys   keyfunc.Keyfunc
}

type supabaseClaims struct {
	jwt.RegisteredClaims
	Role string `json:"role"`
}

func NewSupabaseVerifier(ctx context.Context, projectURL string) (*SupabaseVerifier, error) {
	baseURL, err := normalizeProjectURL(projectURL)
	if err != nil {
		return nil, err
	}

	issuer := baseURL + "/auth/v1"
	failOnInitialRequestError := false
	keys, err := keyfunc.NewDefaultOverrideCtx(
		ctx,
		[]string{issuer + "/.well-known/jwks.json"},
		keyfunc.Override{
			HTTPTimeout:               jwksRequestTimeout,
			NoErrorReturnFirstHTTPReq: &failOnInitialRequestError,
			RefreshInterval:           jwksRefreshInterval,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("load Supabase signing keys: %w", err)
	}

	keySet, err := keys.VerificationKeySet(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Supabase signing keys: %w", err)
	}
	if len(keySet.Keys) == 0 {
		return nil, errors.New("Supabase JWKS contains no asymmetric signing keys")
	}

	return &SupabaseVerifier{issuer: issuer, keys: keys}, nil
}

func (v *SupabaseVerifier) Verify(ctx context.Context, accessToken string) (string, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return "", ErrInvalidAccessToken
	}

	claims := new(supabaseClaims)
	token, err := jwt.ParseWithClaims(
		accessToken,
		claims,
		v.keys.KeyfuncCtx(ctx),
		jwt.WithValidMethods([]string{
			jwt.SigningMethodES256.Alg(),
			jwt.SigningMethodRS256.Alg(),
		}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience("authenticated"),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(tokenClockSkew),
	)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidAccessToken, err)
	}
	if !token.Valid {
		return "", ErrInvalidAccessToken
	}
	if claims.IssuedAt == nil ||
		claims.Role != "authenticated" ||
		strings.TrimSpace(claims.Subject) == "" {
		return "", ErrInvalidAccessToken
	}

	return strings.TrimSpace(claims.Subject), nil
}

func normalizeProjectURL(projectURL string) (string, error) {
	projectURL = strings.TrimSpace(projectURL)
	if projectURL == "" {
		return "", ErrSupabaseURLRequired
	}

	parsed, err := url.Parse(projectURL)
	if err != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("invalid SUPABASE_URL")
	}

	return strings.TrimRight(parsed.String(), "/"), nil
}
