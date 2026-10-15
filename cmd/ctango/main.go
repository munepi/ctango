// Command ctango works with CTAN from the command line.
package main

import (
	"os"
	"runtime/debug"

	"github.com/munepi/ctango/internal/cmd"
	"github.com/munepi/ctango/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = ""

func main() {
	f := &cmd.Factory{IO: ui.System(), Version: buildVersion()}
	os.Exit(cmd.Execute(f, os.Args[1:]))
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
