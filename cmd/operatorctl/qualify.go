//go:build linux || darwin

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/livequalification"
)

func qualifyCommand(args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("qualify", flag.ContinueOnError)
	f.SetOutput(stderr)
	plan := f.String("plan", "", "private absolute-path qualification plan")
	if f.Parse(args) != nil || f.NArg() != 0 || *plan == "" {
		return 2
	}
	prepared, err := livequalification.Load(*plan)
	if err != nil {
		fmt.Fprintln(stderr, "invalid qualification plan or private configuration")
		return 2
	}
	id := livequalification.Identity{Version: operatorVersion, SourceCommit: releaseSourceCommit, ContractVersion: contractstore.SupportedVersion, ContractDigest: contractstore.SupportedDigest}
	if json.NewEncoder(stdout).Encode(prepared.Check(id)) != nil {
		return 1
	}
	return 0
}
