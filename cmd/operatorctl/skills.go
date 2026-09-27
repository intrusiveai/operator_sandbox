//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/skills"
)

func skillCommand(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: operatorctl skill keygen|build|import|check [options]")
		return 2
	}
	command := args[0]
	flags := flag.NewFlagSet("skill "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	config := flags.String("config", defaults.ConfigFile, "installed administrator configuration")
	var project, source, digest *string
	switch command {
	case "keygen":
	case "build":
		project = flags.String("project", "", "project identifier")
		source = flags.String("source", "", "instruction-only skill directory")
	case "import":
		source = flags.String("source", "", "signed bundle directory from this installation")
	case "check":
		digest = flags.String("skill", "", "installed manifest digest")
	default:
		fmt.Fprintln(stderr, "unknown skill command")
		return 2
	}
	if flags.Parse(args[1:]) != nil {
		return 2
	}
	if flags.NArg() != 0 || *config == "" || (project != nil && *project == "") || (source != nil && *source == "") || (digest != nil && *digest == "") {
		fmt.Fprintln(stderr, "missing or invalid skill arguments")
		return 2
	}
	loaded, err := hostconfig.Load(*config, defaults)
	if err != nil {
		fmt.Fprintln(stderr, "cannot load host configuration:", err)
		return 1
	}
	keyDirectory := filepath.Join(filepath.Dir(loaded.Path), "skill-signing")
	if command == "keygen" {
		keyID, err := skills.Keygen(ctx, keyDirectory)
		if err != nil {
			fmt.Fprintln(stderr, "cannot create skill key:", err)
			return 1
		}
		if json.NewEncoder(stdout).Encode(map[string]string{"api_version": "operator.dev/skill-key-receipt/v1alpha1", "key_id": keyID, "directory": keyDirectory}) != nil {
			return 1
		}
		return 0
	}
	c := loaded.Config.Contract
	installed, err := contractstore.Load(ctx, c.Directory, contracts.PackageIdentity{Version: c.Version, Digest: c.Digest})
	if err != nil {
		fmt.Fprintln(stderr, "cannot verify installed contract:", err)
		return 1
	}
	store := filepath.Join(loaded.Config.State.Root, "skills")
	var b *skills.Bundle
	if source != nil {
		dir, e := filepath.Abs(*source)
		if e != nil {
			fmt.Fprintln(stderr, "invalid skill source")
			return 2
		}
		if command == "build" {
			b, err = skills.Build(ctx, installed.Protocol(), keyDirectory, *project, dir)
		} else {
			b, err = skills.Read(ctx, installed.Protocol(), keyDirectory, dir)
		}
		if err == nil {
			err = b.Install(ctx, installed.Protocol(), keyDirectory, store)
		}
	} else {
		b, err = skills.LoadInstalled(ctx, installed.Protocol(), keyDirectory, store, *digest)
	}
	if err != nil {
		fmt.Fprintln(stderr, "skill validation failed:", err)
		return 1
	}
	if json.NewEncoder(stdout).Encode(b.Receipt()) != nil {
		return 1
	}
	return 0
}
