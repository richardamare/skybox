package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/richardamare/skybox"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("skybox", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if !*showVersion {
		flags.Usage()
		return 2
	}
	fmt.Fprintf(stdout, "skybox %s\n", skybox.Version)
	return 0
}
