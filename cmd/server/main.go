// Command server is the entrypoint for running headscale on Eyevinn Open
// Source Cloud's golang-runner. The runner builds cmd/server first and execs
// the binary with $PORT injected via the environment, so this wrapper applies
// the env defaults the runner requires and then delegates to headscale's
// existing serve path. Operator-provided environment variables always win.
package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/juanfont/headscale/cmd/headscale/cli"
)

const noiseKeyPath = "/tmp/noise_private.key"

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	setDefault("HEADSCALE_LISTEN_ADDR", "0.0.0.0:"+port)
	setDefault("HEADSCALE_DNS_OVERRIDE_LOCAL_DNS", "false")
	setDefault("HEADSCALE_DNS_MAGIC_DNS", "false")
	setDefault("HEADSCALE_PREFIXES_V4", "100.64.0.0/10")
	setDefault("HEADSCALE_PREFIXES_V6", "fd7a:115c:a1e0::/48")
	setDefault("HEADSCALE_POLICY_MODE", "database")
	setDefault("HEADSCALE_DATABASE_TYPE", "postgres")
	setDefault("HEADSCALE_UNIX_SOCKET", "/tmp/headscale.sock")
	setDefault("HEADSCALE_DERP_URLS", "https://controlplane.tailscale.com/derpmap/default")

	err := applyDatabaseURL(os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid DATABASE_URL:", err)
		os.Exit(1)
	}

	err = materializeNoiseKey(os.Getenv("HEADSCALE_NOISE_PRIVATE_KEY"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "writing noise private key:", err)
		os.Exit(1)
	}

	os.Args = []string{"headscale", "serve"}

	cli.Execute()
}

func setDefault(key, val string) {
	if os.Getenv(key) == "" {
		os.Setenv(key, val)
	}
}

func applyDatabaseURL(raw string) error {
	if raw == "" {
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parsing DATABASE_URL: %w", err)
	}

	setDefault("HEADSCALE_DATABASE_POSTGRES_HOST", u.Hostname())
	setDefault("HEADSCALE_DATABASE_POSTGRES_PORT", u.Port())
	setDefault("HEADSCALE_DATABASE_POSTGRES_NAME", strings.TrimPrefix(u.Path, "/"))
	setDefault("HEADSCALE_DATABASE_POSTGRES_USER", u.User.Username())

	if pass, ok := u.User.Password(); ok {
		setDefault("HEADSCALE_DATABASE_POSTGRES_PASS", pass)
	}

	if sslmode := u.Query().Get("sslmode"); sslmode != "" {
		setDefault("HEADSCALE_DATABASE_POSTGRES_SSL", sslmode)
	}

	return nil
}

func materializeNoiseKey(value string) error {
	if value == "" {
		setDefault("HEADSCALE_NOISE_PRIVATE_KEY_PATH", "/usercontent/noise_private.key")

		return nil
	}

	err := os.WriteFile(noiseKeyPath, []byte(value), 0o600) //nolint:gosec // fixed path, not user-controlled
	if err != nil {
		return fmt.Errorf("writing %s: %w", noiseKeyPath, err)
	}

	os.Setenv("HEADSCALE_NOISE_PRIVATE_KEY_PATH", noiseKeyPath)

	return nil
}
