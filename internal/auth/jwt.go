// Package auth provides JWT authentication utilities for SoundMap services.
package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey int

const (
	claimsKey contextKey = iota
)

// Claims represents the JWT claims for SoundMap.
type Claims struct {
	jwt.RegisteredClaims
	Role string `json:"role,omitempty"`
}

// ValidateToken validates a JWT token string and returns the claims.
// This function DOES NOT use jwt.WithoutClaimsValidation() - it validates
// the exp claim and other standard claims by default (golang-jwt v5 behavior).
func ValidateToken(tokenString string, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

// GenerateToken creates a new JWT token for testing purposes.
func GenerateToken(subject string, role string, secret string, expiry time.Duration) (string, error) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Role: role,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ExtractTokenFromMetadata extracts the Bearer token from gRPC metadata.
func ExtractTokenFromMetadata(md metadata.MD) string {
	values := md.Get("authorization")
	if len(values) == 0 {
		return ""
	}

	authHeader := values[0]
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}

	return parts[1]
}

// WithClaims adds claims to a context.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// GetClaims retrieves claims from a context.
func GetClaims(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	return claims, ok
}
