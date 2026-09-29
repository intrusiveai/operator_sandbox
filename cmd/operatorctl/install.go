//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/hostrelease"
)

func installCommand(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	if len(args) > 0 && args[0] == "provision-linux" {
		f := flag.NewFlagSet("install provision-linux", flag.ContinueOnError)
		f.SetOutput(stderr)
		grant := f.Bool("grant-docker-access", false, "explicitly add operator to the docker group")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 {
			return 2
		}
		result, err := hostrelease.ProvisionLinux(ctx, *grant)
		if err != nil {
			fmt.Fprintln(stderr, "Linux provisioning failed:", err)
			return 1
		}
		if json.NewEncoder(stdout).Encode(result) != nil {
			return 1
		}
		return 0
	}
	f := flag.NewFlagSet("install", flag.ContinueOnError)
	f.SetOutput(stderr)
	rootDefault := "/opt/operator"
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		rootDefault = filepath.Join(home, "Library", "Application Support", "Operator", "installation")
	}
	root := f.String("root", rootDefault, "private installation directory")
	archive := f.String("archive", "", "local signed release archive")
	keyring := f.String("keyring", "", "independently trusted public keyring")
	verifier := f.String("gpgv", "", "absolute verifier path; default administrator PATH")
	config := f.String("config", defaults.ConfigFile, "private administrator configuration")
	state := f.String("state-root", defaults.StateRoot, "retained campaign state directory")
	image := f.String("image", "", "administrator-installed local harness image selector")
	downgrade := f.Bool("allow-downgrade", false, "explicitly activate an older signed release")
	if f.Parse(args) != nil || f.NArg() != 0 || *archive == "" || *keyring == "" || *image == "" {
		return 2
	}
	if runtime.GOOS == "darwin" && os.Geteuid() == 0 {
		fmt.Fprintln(stderr, "macOS installation must run as the Docker Desktop user")
		return 1
	}
	var err error
	if *verifier == "" {
		*verifier, err = exec.LookPath("gpgv")
	}
	if err != nil {
		fmt.Fprintln(stderr, "install requires gpgv")
		return 1
	}
	result, err := hostrelease.Install(ctx, hostrelease.InstallOptions{Root: *root, Archive: *archive, Verifier: *verifier, Keyring: *keyring, ConfigFile: *config, StateRoot: *state, Image: *image, AllowDowngrade: *downgrade})
	if err != nil {
		fmt.Fprintln(stderr, "install failed:", err)
		return 1
	}
	if json.NewEncoder(stdout).Encode(result) != nil {
		return 1
	}
	return 0
}
