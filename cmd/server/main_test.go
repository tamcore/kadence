package main

import (
	"reflect"
	"testing"

	"github.com/tamcore/kadence/cmd/server/serve"
)

func TestRunnerForFileBridgeCommand(t *testing.T) {
	got := reflect.ValueOf(runnerFor([]string{"file-bridge"})).Pointer()
	want := reflect.ValueOf(serve.RunFileBridge).Pointer()
	if got != want {
		t.Fatal("runnerFor(file-bridge) did not select serve.RunFileBridge")
	}
}
