package config

import (
	"os"
	"strconv"
)

type Config struct {
	Addr          string
	ServiceSecret string
	WebhookSecret string
	DatabaseURL   string
	RedisURL      string
	AMQPURL       string
	DispatchRate  int
}

func Load() Config {
	return Config{
		Addr:          env("ANTREQUEUE_ADDR", ":8090"),
		ServiceSecret: env("ANTREQUEUE_SERVICE_SECRET", ""),
		WebhookSecret: env("ANTREQUEUE_WEBHOOK_SECRET", ""),
		DatabaseURL:   env("ANTREQUEUE_DATABASE_URL", ""),
		RedisURL:      env("ANTREQUEUE_REDIS_URL", ""),
		AMQPURL:       env("ANTREQUEUE_AMQP_URL", ""),
		DispatchRate:  envInt("ANTREQUEUE_DISPATCH_RATE", 50),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
