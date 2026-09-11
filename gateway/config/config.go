package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DBHost                 string
	DBPort                 string
	DBUser                 string
	DBPassword             string
	DBName                 string
	JWTSecret              string
	ServerPort             string
	ChatServiceURLs        []string
	PresenceServiceURL     string
	NotificationServiceURL string
	MediaServiceURL        string

	LBHealthIntervalSecs int

	// Proves to notification-service and media-service that a request came
	// through here; they trust the X-User-ID it travels with on that basis.
	GatewayKey string

	EdgeCacheMaxBytes  int64
	EdgeCacheMaxObject int64
}

func LoadConfig() *Config {
	return &Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "3307"),
		DBUser:     getEnv("DB_USER", "root"),
		DBPassword: getEnv("DB_PASSWORD", "root"),
		DBName:     getEnv("DB_NAME", "chat_gateway_db"),
		JWTSecret:  requireEnv("JWT_SECRET"),
		ServerPort: getEnv("SERVER_PORT", "8000"),
		// Comma-separated. One entry is the month 1-2 setup; two or more is
		// month 3, where the gateway load-balances across them.
		ChatServiceURLs: getEnvList("CHAT_SERVICE_URLS", []string{"http://localhost:8001"}),

		PresenceServiceURL:     getEnv("PRESENCE_SERVICE_URL", "http://localhost:8002"),
		NotificationServiceURL: getEnv("NOTIFICATION_SERVICE_URL", "http://localhost:8003"),
		MediaServiceURL:        getEnv("MEDIA_SERVICE_URL", "http://localhost:8004"),

		LBHealthIntervalSecs: getEnvInt("LB_HEALTH_INTERVAL_SECONDS", 5),

		GatewayKey: requireEnv("GATEWAY_SHARED_KEY"),

		EdgeCacheMaxBytes:  int64(getEnvInt("EDGE_CACHE_MAX_BYTES", 64<<20)),
		EdgeCacheMaxObject: int64(getEnvInt("EDGE_CACHE_MAX_OBJECT_BYTES", 5<<20)),
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

func getEnvInt(key string, fallback int) int {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("%s is not a positive integer (%q), falling back to %d", key, value, fallback)
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
