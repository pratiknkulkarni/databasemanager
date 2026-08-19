package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Set by the linker for a release build. See the LDFLAGS in the Makefile.
// A `go install` leaves them empty, which is why buildInfo falls back to the
// module metadata the toolchain stamps in on its own.
var (
	version = ""
	commit  = ""
	date    = ""
)

// buildInfo prefers the linker-injected values and falls back to what the Go
// toolchain records by itself: the module version for `go install`, and the VCS
// stamps for a plain `go build` inside a git checkout.
func buildInfo() (v, c, d string, dirty bool) {
	v, c, d = version, commit, date

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v, c, d, false
	}

	if v == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if c == "" {
				c = s.Value
			}
		case "vcs.time":
			if d == "" {
				d = s.Value
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return v, c, d, dirty
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		Args:  cobra.NoArgs,
		// Overrides the root's PersistentPreRunE. Printing a version string
		// should not require a readable config file and a reachable Infisical.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, c, d, dirty := buildInfo()
			if v == "" {
				v = "dev"
			}
			// `git describe --dirty` already says so; don't say it twice.
			if dirty && !strings.Contains(v, "dirty") {
				v += " (dirty)"
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "databasemanager %s\n", v)
			if c != "" {
				fmt.Fprintf(out, "  commit:  %s\n", c)
			}
			if d != "" {
				fmt.Fprintf(out, "  built:   %s\n", d)
			}
			fmt.Fprintf(out, "  go:      %s\n", runtime.Version())
			fmt.Fprintf(out, "  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}
