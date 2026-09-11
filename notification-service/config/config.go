package config

import (
	"log"
	"os"
	"strconv"
)

type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	ServerPort string

	// End-user requests arrive only through the gateway, which has already
	// validated the Bearer token and forwards the identity. This key is how
	// this service knows the identity really came from the gateway.
	GatewayKey string
	// chat-service authenticates its message events with this one.
	NotificationAPIKey string

	PresenceServiceURL string
	PresenceAPIKey     string

	DeliveryWorkers int
}

func LoadConfig() *Config {
	return &Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "3307"),
		DBUser:     getEnv("DB_USER", "root"),
		DBPassword: getEnv("DB_PASSWORD", "root"),
		DBName:     getEnv("DB_NAME", "notification_service_db"),

		ServerPort: getEnv("SERVER_PORT", "8003"),

		GatewayKey:         requireEnv("GATEWAY_SHARED_KEY"),
		NotificationAPIKey: requireEnv("NOTIFICATION_API_KEY"),

		PresenceServiceURL: getEnv("PRESENCE_SERVICE_URL", "http://localhost:8002"),
		PresenceAPIKey:     requireEnv("PRESENCE_API_KEY"),

		// Deliveries are independent of each other, so unlike the presence
		// worker this pool can be as wide as the receivers can take.
		DeliveryWorkers: getEnvInt("DELIVERY_WORKERS", 4),
	}
}

// requireEnv fails fast: a predictable default for a shared secret would let
// anyone impersonate the gateway (and so any user) or chat-service.
func requireEnv(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		log.Fatalf("%s must be set (no default - a predictable secret would allow impersonation)", key)
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
