package observability

import (
	"log"
	"os"
	"strconv"
	"time"
)

func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func EnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func EnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func Info(msg string, kv ...any) {
	log.Printf("[INFO] %s %v", msg, kv)
}

func Warn(msg string, kv ...any) {
	log.Printf("[WARN] %s %v", msg, kv)
}

func Error(msg string, kv ...any) {
	log.Printf("[ERROR] %s %v", msg, kv)
}
