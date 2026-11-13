package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/mdpsec/huntdeck/internal/config"
)

// healthcheck lets Docker probe the server without curl in the image.
func healthcheck(_, stderr io.Writer) int {
	cfg, err := config.Load(config.OSSource)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: invalid configuration: %v\n", err)
		return 1
	}
	url, err := healthURL(cfg.Listen)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck: %s returned %s\n", url, res.Status)
		return 1
	}
	return 0
}

// healthURL turns the listen address into a URL reachable from inside the
// same container: wildcard hosts are replaced with loopback.
func healthURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("listen address %q: %w", listen, err)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}
