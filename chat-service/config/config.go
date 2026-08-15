package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	JWTSecret  string
	ServerPort string

	PresenceServiceURL    string
	PresenceAPIKey        string
	PresenceHeartbeatSecs int

	WSAllowedOrigins []string
}

func LoadConfig() *Config {
	return &Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "3307"),
		DBUser:     getEnv("DB_USER", "root"),
		DBPassword: getEnv("DB_PASSWORD", "root"),
		// chat-service owns chat_service_db; the gateway owns chat_gateway_db.
		// Same MySQL container, separate schemas - see db/chat_schema.sql.
		DBName: getEnv("DB_NAME", "chat_service_db"),

		JWTSecret:  requireEnv("JWT_SECRET"),
		ServerPort: getEnv("SERVER_PORT", "8001"),

		PresenceServiceURL:    getEnv("PRESENCE_SERVICE_URL", "http://localhost:8002"),
		PresenceAPIKey:        requireEnv("PRESENCE_API_KEY"),
		PresenceHeartbeatSecs: getEnvInt("PRESENCE_HEARTBEAT_SECONDS", 30),

		WSAllowedOrigins: getEnvList("WS_ALLOWED_ORIGINS", []string{"http://localhost:3000", "http://localhost:8000"}),
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
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

func getEnvList(key string, fallback []string) []string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}

	var values []string
	for part := range strings.SplitSeq(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}

	return values
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
