package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	AppEnv  string
	HTTPAddr string

	GitHubWebhookSecret string
	OpenClawAgentToken  string
	AdminToken          string

	SQLitePath string

	EventMaxRetry         int
	EventRetryInitialSecs int
	EventRetryMaxSecs     int
	EventDeliveryBatchSize int

	WSWriteTimeout time.Duration
	WSReadTimeout  time.Duration
	WSPingInterval time.Duration

	MaxBodyBytes int64

	LogLevel string
}

func Load() *Config {
	return &Config{
		AppEnv:  getEnv("APP_ENV", "production"),
		HTTPAddr: getEnv("HTTP_ADDR", "0.0.0.0:8080"),

		GitHubWebhookSecret: getEnv("GITHUB_WEBHOOK_SECRET", "change-me"),
		OpenClawAgentToken:  getEnv("OPENCLAW_AGENT_TOKEN", "change-me"),
		AdminToken:          getEnv("ADMIN_TOKEN", "change-me"),

		SQLitePath: getEnv("SQLITE_PATH", "/data/relay.db"),

		EventMaxRetry:          getEnvInt("EVENT_MAX_RETRY", 10),
		EventRetryInitialSecs:  getEnvInt("EVENT_RETRY_INITIAL_SECONDS", 5),
		EventRetryMaxSecs:      getEnvInt("EVENT_RETRY_MAX_SECONDS", 300),
		EventDeliveryBatchSize: getEnvInt("EVENT_DELIVERY_BATCH_SIZE", 10),

		WSWriteTimeout: time.Duration(getEnvInt("WS_WRITE_TIMEOUT_SECONDS", 10)) * time.Second,
		WSReadTimeout:  time.Duration(getEnvInt("WS_READ_TIMEOUT_SECONDS", 90)) * time.Second,
		WSPingInterval: time.Duration(getEnvInt("WS_PING_INTERVAL_SECONDS", 30)) * time.Second,

		MaxBodyBytes: int64(getEnvInt("MAX_BODY_BYTES", 26214400)),

		LogLevel: getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}
