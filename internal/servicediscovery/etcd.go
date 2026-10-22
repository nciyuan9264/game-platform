package servicediscovery

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/server"
	etcdregistry "github.com/kitex-contrib/registry-etcd"
)

var psmPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*\.[a-z][a-z0-9_-]*\.[a-z][a-z0-9_-]*$`)

// ServerOptions returns PSM metadata and optional etcd registration options.
func ServerOptions(defaultPSM string) ([]server.Option, string, error) {
	key := "CONTROL_RPC_PSM"
	if defaultPSM == "game.platform.rpc" {
		key = "GAME_PLATFORM_RPC_PSM"
	}
	psm := env(key, defaultPSM)
	if err := ValidatePSM(psm); err != nil {
		return nil, "", err
	}
	options := []server.Option{server.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{
		ServiceName: psm,
		Tags: map[string]string{
			"environment": env("PAM_ENV", "local"),
			"version":     os.Getenv("SERVICE_VERSION"),
			"idl_commit":  os.Getenv("PAM_IDL_COMMIT"),
			"instance_id": os.Getenv("NODE_ID"),
		},
	})}
	endpoints := splitCSV(os.Getenv("ETCD_ENDPOINTS"))
	if len(endpoints) == 0 {
		return options, psm, nil
	}
	registry, err := etcdregistry.NewEtcdRegistry(endpoints, registryOptions()...)
	if err != nil {
		return nil, "", err
	}
	return append(options, server.WithRegistry(registry)), psm, nil
}

// Resolver returns an environment-isolated etcd resolver, or nil when discovery is disabled.
func Resolver() (discovery.Resolver, error) {
	endpoints := splitCSV(os.Getenv("ETCD_ENDPOINTS"))
	if len(endpoints) == 0 {
		return nil, nil
	}
	options := registryOptions()
	return etcdregistry.NewEtcdResolver(endpoints, options...)
}

// ServiceName returns and validates an environment-configurable PSM.
func ServiceName(key, fallback string) (string, error) {
	psm := env(key, fallback)
	if err := ValidatePSM(psm); err != nil {
		return "", err
	}
	return psm, nil
}

// ValidatePSM enforces the shared domain.service.role naming convention.
func ValidatePSM(psm string) error {
	if !psmPattern.MatchString(strings.TrimSpace(psm)) {
		return fmt.Errorf("PSM %q must contain exactly three lower-case segments", psm)
	}
	return nil
}

func registryOptions() []etcdregistry.Option {
	prefix := strings.Trim(env("PAM_SERVICE_REGISTRY_PREFIX", "pam/services"), "/") + "/" + strings.Trim(env("PAM_ENV", "local"), "/")
	options := []etcdregistry.Option{etcdregistry.WithEtcdServicePrefix(prefix)}
	if username := strings.TrimSpace(os.Getenv("ETCD_USERNAME")); username != "" {
		options = append(options, etcdregistry.WithAuthOpt(username, os.Getenv("ETCD_PASSWORD")))
	}
	return options
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
