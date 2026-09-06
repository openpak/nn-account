// Package config loads adapter configuration. Missing required values fail
// startup (PRD §8).
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL      string // required: adapter's own PostgreSQL database
	CoreAddress      string // required: account core gRPC address
	CoreInternalKey  string // required: bearer key for core internal APIs
	GRPCListenAddr   string // adapter gRPC (Pretendo-compatible v2)
	HTTPListenAddr   string // NNAS/NASC HTTP
	GRPCAPIKey       string // required: key consumers (friends) must present
	NNASDomain       string
	NASCDomain       string
	CDNBaseURL       string // Mii asset base URL
	Environment      string
	AllowedHostnames []string // empty = allow all (development)
}

func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:      os.Getenv("NN_ACCOUNT_DATABASE_URL"),
		CoreAddress:      envOr("NN_ACCOUNT_CORE_ADDR", "localhost:7000"),
		CoreInternalKey:  os.Getenv("NN_ACCOUNT_CORE_KEY"),
		GRPCListenAddr:   envOr("NN_ACCOUNT_GRPC_ADDR", ":7001"),
		HTTPListenAddr:   envOr("NN_ACCOUNT_HTTP_ADDR", ":8001"),
		GRPCAPIKey:       os.Getenv("NN_ACCOUNT_GRPC_API_KEY"),
		NNASDomain:       os.Getenv("NN_ACCOUNT_NNAS_DOMAIN"),
		NASCDomain:       os.Getenv("NN_ACCOUNT_NASC_DOMAIN"),
		CDNBaseURL:       envOr("NN_ACCOUNT_CDN_BASE_URL", "https://cdn.openpak.example"),
		Environment:      envOr("NN_ACCOUNT_ENVIRONMENT", "production"),
		AllowedHostnames: nil,
	}
	if h := os.Getenv("NN_ACCOUNT_ALLOWED_HOSTNAMES"); h != "" {
		c.AllowedHostnames = strings.Split(h, ",")
	}
	var missing []string
	for name, val := range map[string]string{
		"NN_ACCOUNT_DATABASE_URL": c.DatabaseURL,
		"NN_ACCOUNT_CORE_KEY":     c.CoreInternalKey,
		"NN_ACCOUNT_GRPC_API_KEY": c.GRPCAPIKey,
	} {
		if val == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrMissingConfig, strings.Join(missing, ", "))
	}
	return c, nil
}

var ErrMissingConfig = errors.New("missing required configuration")

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
