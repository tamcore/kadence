//go:build prodfrontend

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:build
var prodFS embed.FS

func init() {
	sub, err := fs.Sub(prodFS, "build")
	if err != nil {
		panic(err)
	}
	FS = sub
}
