package config

import (
	"log"
	"os"
)

type Config struct {
	DBHost             string
	DBPort             string
	DBUser             string
	DBPassword         string
	DBName             string
	JWTSecret          string
	ServerPort         string
	ChatServiceURL     string
	PresenceServiceURL string
}

func LoadConfig() *Config {
	return &Config{
		DBHost:         getEnv("DB_HOST", "localhost"),
		DBPort:         getEnv("DB_PORT", "3307"),
		DBUser:         getEnv("DB_USER", "root"),
		DBPassword:     getEnv("DB_PASSWORD", "root"),
		DBName:         getEnv("DB_NAME", "chat_gateway_db"),
		JWTSecret:      requireEnv("JWT_SECRET"),
		ServerPort:     getEnv("SERVER_PORT", "8000"),
		ChatServiceURL: getEnv("CHAT_SERVICE_URL", "http://localhost:8001"),

		PresenceServiceURL: getEnv("PRESENCE_SERVICE_URL", "http://localhost:8002"),
	}
}

// requireEnv has no fallback on purpose: a predictable default signing
// key would let anyone forge valid tokens for any user_id/role.
func requireEnv(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		log.Fatalf("%s must be set (no default - a predictable secret would allow token forgery)", key)
	}
	return value
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
