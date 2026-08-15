package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

const UserIDKey = "user_id"
const RoleKey = "role"

// This service does not issue tokens - only the gateway does. It
// independently validates them against the same JWT_SECRET, which is
// the standard microservice pattern: share a secret/config value, not
// a code library, across independently deployable services.
var jwtSecret []byte

func InitJWT(secret string) {
	jwtSecret = []byte(secret)
}

type Claims struct {
	UserID int64
	Role   string
}

func ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Confirm the algorithm rather than trusting the token's own header:
		// without this, the keyfunc hands the HMAC secret to whatever method the
		// token asks for. Not exploitable while everything here is HMAC, but it
		// is the footgun that arms itself the day an asymmetric key appears.
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		return jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid or expired token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}

	userIDFloat, ok := claims["user_id"].(float64)
	if !ok {
		return nil, errors.New("invalid user id in token")
	}

	role, _ := claims["role"].(string)

	return &Claims{
		UserID: int64(userIDFloat),
		Role:   role,
	}, nil
}

// JWTMiddleware guards plain HTTP routes (e.g. /rooms) with a Bearer token.
// /ws validates the same header inline - see ws.Handler.Serve - because it needs
// the claims before the upgrade rather than in the Echo context after it.
func JWTMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		authHeader := c.Request().Header.Get("Authorization")
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if authHeader == "" || tokenString == authHeader {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
		}

		claims, err := ValidateToken(tokenString)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": err.Error()})
		}

		c.Set(UserIDKey, claims.UserID)
		c.Set(RoleKey, claims.Role)

		return next(c)
	}
}
