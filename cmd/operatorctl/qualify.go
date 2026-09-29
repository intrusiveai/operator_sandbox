//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/livequalification"
)

func qualifyCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("qualify", flag.ContinueOnError)
	f.SetOutput(stderr)
	plan := f.String("plan", "", "private absolute-path qualification plan")
	live := f.Bool("live", false, "explicitly permit bounded live service calls")
	output := f.String("output", "", "new JSONL evidence file in a private directory")
	if f.Parse(args) != nil || f.NArg() != 0 || *plan == "" {
		return 2
	}
	prepared, err := livequalification.Load(*plan)
	if err != nil {
		fmt.Fprintln(stderr, "invalid qualification plan or private configuration")
		return 2
	}
	id := livequalification.Identity{Version: operatorVersion, SourceCommit: releaseSourceCommit, ContractVersion: contractstore.SupportedVersion, ContractDigest: contractstore.SupportedDigest}
	if *live {
		if *output == "" {
			return 2
		}
		file, err := livequalification.CreateEvidence(*output)
		if err != nil {
			fmt.Fprintln(stderr, "cannot create new qualification evidence file")
			return 1
		}
		encoder := json.NewEncoder(file)
		err = prepared.Run(ctx, id, func(record livequalification.Record) error {
			if err := encoder.Encode(record); err != nil {
				return err
			}
			return file.Sync()
		})
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			fmt.Fprintln(stderr, "qualification did not complete successfully; inspect sanitized evidence")
			return 1
		}
		fmt.Fprintln(stdout, "Selected qualification checks completed; inspect evidence for not_run checks.")
		return 0
	}
	if *output != "" {
		return 2
	}
	if json.NewEncoder(stdout).Encode(prepared.Check(id)) != nil {
		return 1
	}
	return 0
}
