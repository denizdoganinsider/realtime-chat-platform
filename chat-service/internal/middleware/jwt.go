package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

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

// JWTMiddleware guards plain HTTP routes (e.g. /rooms) with a Bearer
// token. /ws validates separately via query param - see ws.Handler.Serve -
// since browser WebSocket clients cannot set the Authorization header.
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

		c.Set("user_id", claims.UserID)
		c.Set("role", claims.Role)

		return next(c)
	}
}
