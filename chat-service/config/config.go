package config

import (
	"log"
	"os"
)

type Config struct {
	JWTSecret  string
	ServerPort string
}

func LoadConfig() *Config {
	return &Config{
		JWTSecret:  requireEnv("JWT_SECRET"),
		ServerPort: getEnv("SERVER_PORT", "8001"),
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// requireEnv has no fallback on purpose: a predictable default signing
// key would let anyone forge valid tokens for any user_id/role. Must
// match the gateway's JWT_SECRET exactly - see README.
func requireEnv(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		log.Fatalf("%s must be set (no default - a predictable secret would allow token forgery)", key)
	}
	return value
}
