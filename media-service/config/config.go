package config

import (
	"log"
	"os"
	"strconv"
)

type Config struct {
	ServerPort string
	// Where files live. Content-addressed: <MediaDir>/<sha256> plus a small
	// JSON sidecar with the content type.
	MediaDir string
	// Uploads arrive only through the gateway, which validated the token.
	GatewayKey    string
	MaxUploadByte int64
}

func LoadConfig() *Config {
	return &Config{
		ServerPort:    getEnv("SERVER_PORT", "8004"),
		MediaDir:      getEnv("MEDIA_DIR", "./data/media"),
		GatewayKey:    requireEnv("GATEWAY_SHARED_KEY"),
		MaxUploadByte: getEnvInt64("MAX_UPLOAD_BYTES", 5<<20),
	}
}

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

func getEnvInt64(key string, fallback int64) int64 {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		log.Printf("%s is not a positive integer (%q), falling back to %d", key, value, fallback)
		return fallback
	}

	return parsed
}
