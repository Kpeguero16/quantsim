// Command healthcheck is the HEALTHCHECK for QuantSim's service images.
//
// It exists because those images are distroless: no shell, no wget, no curl,
// nothing for a HEALTHCHECK to run. Step 25 recorded that and left the images
// with no healthcheck at all, so `docker compose ps` reported "Up" for a
// process that had stopped answering.
//
// The alternative was a -healthcheck flag in each service's main.go: six edits
// to six files that must stay identical, in six packages that otherwise have
// nothing to do with each other. This is one binary, built once in the builder
// stage and copied into every image.
//
// What it buys is visibility, NOT recovery. Docker does not restart an
// unhealthy container -- only Swarm and Kubernetes do -- so `restart:
// unless-stopped` still covers a crash and nothing covers a hang. What changes
// is that `docker compose ps` stops saying everything is fine. SPEC.md
// Step 26 §2.6, and §6 for why recovery is deliberately not solved here.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// timeout bounds the whole check. It must stay below the HEALTHCHECK interval
// in Dockerfile.service, or a slow check overlaps the next one and the two
// argue about the container's state.
const timeout = 2 * time.Second

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		// Every service defaults its own PORT, and compose sets it
		// explicitly. Reaching this means neither happened, which is worth
		// failing on rather than guessing a port and reporting healthy
		// because something else answered.
		fmt.Fprintln(os.Stderr, "healthcheck: PORT is not set")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 127.0.0.1 and not the service's BIND_ADDR. This runs inside the
	// container, so loopback is always right and is reachable whether the
	// service bound 0.0.0.0 or loopback itself.
	url := "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: %s returned %d\n", url, resp.StatusCode)
		os.Exit(1)
	}
}
