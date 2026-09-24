//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/dockercontrol"
	"github.com/intrusive-ai/operator-sandbox/internal/termination"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "campaign" || args[1] != "terminate" {
		fmt.Fprintln(stderr, "usage: operatorctl campaign terminate --campaign ID [--state-root DIR] [--mode immediate] [--reason user-request] [--request-id HEX32] [--docker-bin PATH]")
		return 2
	}
	flags := flag.NewFlagSet("campaign terminate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("campaign", "", "campaign ID")
	root := flags.String("state-root", "", "installed private state root")
	mode := flags.String("mode", "immediate", "termination mode (immediate)")
	reason := flags.String("reason", "user-request", "bounded host reason identifier")
	request := flags.String("request-id", "", "optional 32-character lowercase hex request ID")
	executable := flags.String("docker-bin", "", "absolute Docker CLI executable; defaults to PATH lookup")
	if err := flags.Parse(args[2:]); err != nil {
		return 2
	}
	if *request == "" {
		*request = termination.NewRequestID()
	}
	if flags.NArg() != 0 || *id == "" || *mode != "immediate" || !campaign.ValidStopRequest(*request, *reason) {
		fmt.Fprintln(stderr, "invalid termination arguments")
		return 2
	}
	if *root == "" {
		*root = "/var/lib/operator"
		if runtime.GOOS == "darwin" {
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintln(stderr, "cannot resolve installed state root")
				return 2
			}
			*root = filepath.Join(home, "Library", "Application Support", "Operator", "data")
		}
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
