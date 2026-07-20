// Command server is the Kadence HTTP server entrypoint.
package main

import (
	"log/slog"
	"os"

	"github.com/tamcore/kadence/cmd/server/serve"
)

func main() {
	run := serve.Run
	if len(os.Args) > 1 && os.Args[1] == "wait-for-db" {
		run = serve.WaitForDB
	}

	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}
