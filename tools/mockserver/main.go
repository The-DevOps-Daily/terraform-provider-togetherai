// Command mockserver runs the in-memory Together AI mock API on a local port,
// so the examples and the acceptance tests can run without a real API key:
//
//	go run ./tools/mockserver
//	export TOGETHER_BASE_URL=<printed URL> TOGETHER_API_KEY=mock-api-key
package main

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/mockapi"
)

func main() {
	m := mockapi.New()
	defer m.Close()
	fmt.Println(m.URL())
	fmt.Fprintf(os.Stderr, "mock Together AI API at %s (API key %q). Ctrl-C to stop.\n", m.URL(), mockapi.APIKey)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
