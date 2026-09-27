//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var home string
	if runtime.GOOS == "darwin" {
		home, _ = os.UserHomeDir()
	}
	// Unavailable host defaults fail when used, but must not prevent termination
	// with explicit recovery paths (for example a macOS process without HOME).
	defaults, _ := hostconfig.Defaults(runtime.GOOS, home)
	return runWithDefaults(ctx, args, stdout, stderr, defaults)
}

func runWithDefaults(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	if len(args) >= 2 && args[0] == "campaign" && (args[1] == "prepare" || args[1] == "start") {
		return startCampaign(ctx, args[1], args[2:], stdout, stderr, defaults)
	}
	if len(args) > 0 && args[0] == "_worker" {
		return workerCommand(ctx, args[1:], stdout, stderr)
	}
	if len(args) == 1 && args[0] == "version" {
		if json.NewEncoder(stdout).Encode(map[string]string{"api_version": "operator.dev/version/v1alpha1", "version": operatorVersion}) != nil {
			return 1
		}
		return 0
	}

	if len(args) >= 2 && args[0] == "campaign" && (args[1] == "status" || args[1] == "logs" || args[1] == "wait") {
		return observeCampaign(ctx, args[1], args[2:], stdout, stderr, defaults)
	}
	if len(args) >= 2 && args[0] == "contract" && args[1] == "build" {
		return buildContract(ctx, args[2:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "skill" {
		return skillCommand(ctx, args[1:], stdout, stderr, defaults)
	}
	if len(args) > 0 && (args[0] == "submit" || args[0] == "validate") {
		return submissionCommand(ctx, args, stdout, stderr, defaults)
	}
	if len(args) >= 2 && args[0] == "contract" && args[1] == "check" {
		return checkContract(ctx, args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "config" && args[1] == "check" {
		return checkConfig(args[2:], stdout, stderr, defaults)
	}
	if len(args) < 2 || args[0] != "campaign" || args[1] != "terminate" {
		fmt.Fprintln(stderr, "usage: operatorctl submit --bundle FILE --capabilities FILE [--artifacts DIR] --output DIR [--config PATH]\n       operatorctl validate --run DIR [--config PATH]\n       operatorctl config check [--config PATH]\n       operatorctl contract check --package-dir DIR --package-version VERSION --package-digest SHA256\n       operatorctl campaign terminate --campaign ID [--config PATH] [--state-root DIR] [--mode immediate] [--reason user-request] [--request-id HEX32] [--docker-bin PATH]")
		fmt.Fprintln(stderr, "       operatorctl campaign status|logs|wait --campaign ID [--config PATH] [--state-root DIR]")
		fmt.Fprintln(stderr, "       operatorctl campaign prepare|start --run DIR [--config PATH] [--new-campaign] [--skill DIGEST] [--system-prompt FILE | --system-prompt-append FILE]")
		fmt.Fprintln(stderr, "       operatorctl skill keygen|build|import|check [options]")
		fmt.Fprintln(stderr, "       operatorctl contract build --source DIR --output DIR --package-version VERSION")
		return 2
	}
	flags := flag.NewFlagSet("campaign terminate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("campaign", "", "campaign ID")
	config := flags.String("config", "", "absolute administrator configuration path")
	root := flags.String("state-root", "", "installed private state root")
	mode := flags.String("mode", "immediate", "termination mode (immediate)")
	reason := flags.String("reason", "user-request", "bounded host reason identifier")
	request := flags.String("request-id", "", "optional 32-character lowercase hex request ID")
	executable := flags.String("docker-bin", "", "absolute Docker CLI executable; defaults to PATH lookup")
	if err := flags.Parse(args[2:]); err != nil {
		return 2
	}
	provided := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if *request == "" {
		*request = termination.NewRequestID()
	}
	if flags.NArg() != 0 || *id == "" || *mode != "immediate" || !campaign.ValidStopRequest(*request, *reason) ||
		(provided["config"] && *config == "") || (provided["state-root"] && *root == "") || (provided["docker-bin"] && *executable == "") {
		fmt.Fprintln(stderr, "invalid termination arguments")
		return 2
	}
	// Explicit recovery paths bypass a broken default configuration. An explicit
	// --config always means that exact file must load successfully.
	if *config != "" || *root == "" {
		name := *config
		if name == "" {
			name = defaults.ConfigFile
		}
		loaded, err := hostconfig.Load(name, defaults)
		if err != nil && (*config != "" || !errors.Is(err, os.ErrNotExist)) {
			fmt.Fprintln(stderr, "cannot load host configuration:", err)
			fmt.Fprintln(stderr, "for emergency recovery, omit --config and supply --state-root (and --docker-bin if needed)")
			return 2
		}
		if err == nil {
			if *root == "" {
				*root = loaded.Config.State.Root
			}
			if *executable == "" {
				*executable = loaded.Config.Docker.Executable
			}
		}
	}
	if *root == "" {
		*root = defaults.StateRoot
	}
	if !filepath.IsAbs(*root) {
		fmt.Fprintln(stderr, "state root must be absolute")
		return 2
	}
	docker, err := dockercontrol.New(*executable)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	receipt := termination.New(docker).Terminate(ctx, *root, *id, *request, *reason)
	if err := json.NewEncoder(stdout).Encode(receipt); err != nil {
		return 1
	}
	if !receipt.Successful() {
		return 1
	}
	return 0
}

func checkConfig(args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	flags := flag.NewFlagSet("config check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	name := flags.String("config", defaults.ConfigFile, "absolute administrator configuration path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *name == "" {
		fmt.Fprintln(stderr, "config check requires an installed configuration path and no positional arguments")
		return 2
	}
	loaded, err := hostconfig.Load(*name, defaults)
	if err != nil {
		fmt.Fprintln(stderr, "cannot load host configuration:", err)
		return 2
	}
	result := struct {
		APIVersion string `json:"api_version"`
		Status     string `json:"status"`
		hostconfig.Loaded
	}{"operator.dev/config-check/v1alpha1", "valid", loaded}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return 1
	}
	return 0
}
