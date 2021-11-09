package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const (
	goModFile = "go.mod"
	goSumFile = "go.sum"
)

// verify-mod-tidy verifies that `go mod tidy` has been run to clean up the
// go.mod and go.sum files.
func main() {
	var (
		goBin   string
		timeout time.Duration
	)

	flag.DurationVar(&timeout, "timeout", 0, "timeout for verifying modules are tidy")
	flag.StringVar(&goBin, "goBin", "go", "path to go binary to use for mod tidy check")
	flag.Parse()

	ctx := context.Background()
	if timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	oldGoMod, err := os.ReadFile(goModFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading file %s: %s\n", goModFile, err)
		os.Exit(1)
	}
	oldGoSum, err := os.ReadFile(goSumFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading file %s: %s\n", goSumFile, err)
		os.Exit(1)
	}

	cmd := exec.CommandContext(ctx, goBin, "mod", "tidy")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "mod tidy: %s\n", err)
		os.Exit(1)
	}

	newGoMod, err := os.ReadFile(goModFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading file %s: %s\n", goModFile, err)
		os.Exit(1)
	}
	newGoSum, err := os.ReadFile(goSumFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading file %s: %s\n", goSumFile, err)
		os.Exit(1)
	}

	if !bytes.Equal(oldGoMod, newGoMod) || !bytes.Equal(oldGoSum, newGoSum) {
		fmt.Fprintf(os.Stderr, "%s and/or %s are not tidy - please run `go mod tidy`.\n", goModFile, goSumFile)
		if err := os.WriteFile(goModFile, oldGoMod, 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		if err := os.WriteFile(goSumFile, oldGoSum, 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
