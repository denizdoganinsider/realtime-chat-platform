package config

import (
	"log"
	"os"
	"strconv"
)

type Config struct {
	RedisAddr      string
	JWTSecret      string
	PresenceAPIKey string
	ServerPort     string
	PresenceTTLSec int
}

func LoadConfig() *Config {
	return &Config{
		RedisAddr:      getEnv("REDIS_ADDR", "localhost:6380"),
		JWTSecret:      requireEnv("JWT_SECRET"),
		PresenceAPIKey: requireEnv("PRESENCE_API_KEY"),
		ServerPort:     getEnv("SERVER_PORT", "8002"),
		PresenceTTLSec: getEnvInt("PRESENCE_TTL_SECONDS", 90),
	}
}

// requireEnv fails fast: a predictable default for a shared secret would let
// anyone forge tokens or impersonate chat-service.
func requireEnv(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		log.Fatalf("%s must be set (no default - a predictable secret would allow token forgery)", key)
	}

	return value
}

func getEnv(key string, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}

	return value
}

func getEnvInt(key string, fallback int) int {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Printf("%s is not a valid integer (%q), falling back to %d", key, value, fallback)
		return fallback
	}

	return parsed
}
