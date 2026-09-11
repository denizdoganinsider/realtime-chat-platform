package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

const maxInstanceIDLength = 64

type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	JWTSecret  string
	ServerPort string
	InstanceID string

	PresenceServiceURL    string
	PresenceAPIKey        string
	PresenceHeartbeatSecs int

	WSAllowedOrigins []string
}

func LoadConfig() *Config {
	serverPort := getEnv("SERVER_PORT", "8001")

	// Names this process to presence-service and in its own logs. Two instances
	// on one machine differ only by port, so the port is the default; a real
	// deployment sets something like the pod name.
	instanceID := getEnv("INSTANCE_ID", "chat-"+serverPort)
	if !ValidInstanceID(instanceID) {
		// Fail at startup, not on the first presence event: presence-service
		// answers 400 to a bad id, PresenceClient treats 400 as final, and the
		// instance would run for its whole life with presence silently broken.
		log.Fatalf("INSTANCE_ID %q must match ^[A-Za-z0-9_.-]{1,%d}$ (it becomes part of a Redis member)", instanceID, maxInstanceIDLength)
	}

	return &Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "3307"),
		DBUser:     getEnv("DB_USER", "root"),
		DBPassword: getEnv("DB_PASSWORD", "root"),
		// chat-service owns chat_service_db; the gateway owns chat_gateway_db.
		// Same MySQL container, separate schemas - see db/chat_schema.sql.
		DBName: getEnv("DB_NAME", "chat_service_db"),

		JWTSecret:  requireEnv("JWT_SECRET"),
		ServerPort: serverPort,
		InstanceID: instanceID,

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

// ValidInstanceID is the same rule presence-service enforces on the wire: the
// id sits after a colon in "<user_id>:<instance_id>", so a colon (or anything
// else exotic) in it would make that member ambiguous on the way back out.
func ValidInstanceID(instanceID string) bool {
	if instanceID == "" || len(instanceID) > maxInstanceIDLength {
		return false
	}

	for _, r := range instanceID {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.'
		if !isAllowed {
			return false
		}
	}

	return true
}
