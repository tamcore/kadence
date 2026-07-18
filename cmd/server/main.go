// Command server is the Kadence HTTP server entrypoint.
package main

import (
	"log/slog"
	"os"

	"github.com/tamcore/kadence/cmd/server/serve"
)

func main() {
	if err := serve.Run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}
