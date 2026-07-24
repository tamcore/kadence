// Command server is the Kadence HTTP server entrypoint.
package main

import (
	"log/slog"
	"os"

	"github.com/tamcore/kadence/cmd/server/serve"
)

func main() {
	run := runnerFor(os.Args[1:])

	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func runnerFor(args []string) func() error {
	if len(args) == 0 {
		return serve.Run
	}
	switch args[0] {
	case "wait-for-db":
		return serve.WaitForDB
	case "file-bridge":
		return serve.RunFileBridge
	default:
		return serve.Run
	}
}
