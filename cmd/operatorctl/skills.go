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
		fmt.Fprintln(stderr, "usage: operatorctl skill build|import|check|set|remove [options]")
		return 2
	}
	command := args[0]
	flags := flag.NewFlagSet("skill "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	config := flags.String("config", defaults.ConfigFile, "installed administrator configuration")
	var project, source, digest, loader, output *string
	var selections stringsFlag
	switch command {
	case "build":
		project = flags.String("project", "", "project identifier")
		source = flags.String("source", "", "instruction-only skill directory")
	case "import":
		source = flags.String("source", "", "validated bundle directory")
	case "set":
		loader = flags.String("loader-digest", "", "approved image instruction-loader digest")
		output = flags.String("output", "", "new frozen SkillSetManifest JSON file")
		flags.Var(&selections, "skill", "installed bundle digest (repeatable)")
	case "check", "remove":
		digest = flags.String("skill", "", "installed manifest digest")
	default:
		fmt.Fprintln(stderr, "unknown skill command")
		return 2
	}
	if flags.Parse(args[1:]) != nil {
		return 2
	}
	if flags.NArg() != 0 || *config == "" || (project != nil && *project == "") || (source != nil && *source == "") || (digest != nil && *digest == "") || (loader != nil && *loader == "") || (output != nil && *output == "") {
		fmt.Fprintln(stderr, "missing or invalid skill arguments")
		return 2
	}
	loaded, err := hostconfig.Load(*config, defaults)
	if err != nil {
		fmt.Fprintln(stderr, "cannot load host configuration:", err)
		return 1
	}
	store := filepath.Join(loaded.Config.State.Root, "skills")
	if command == "remove" {
		removed, err := skills.Remove(ctx, store, *digest)
		if err != nil {
			fmt.Fprintln(stderr, "skill removal failed; retry explicit removal:", err)
			return 1
		}
		status := "already_absent"
		if removed {
			status = "removed"
		}
		if json.NewEncoder(stdout).Encode(map[string]string{"api_version": "operator.dev/skill-removal/v1alpha1", "manifest_digest": *digest, "status": status}) != nil {
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
	if command == "set" {
		selected, err := skills.Select(ctx, installed.Protocol(), store, *loader, selections)
		if err != nil {
			fmt.Fprintln(stderr, "skill selection failed:", err)
			return 1
		}
		name, err := filepath.Abs(*output)
		if err != nil {
			return 2
		}
		if err = selected.Save(ctx, name); err != nil {
			fmt.Fprintln(stderr, "cannot publish frozen skill set:", err)
			return 1
		}
		if json.NewEncoder(stdout).Encode(map[string]string{"api_version": "operator.dev/skill-set-receipt/v1alpha1", "manifest_digest": contracts.RawDigest(selected.Manifest())}) != nil {
			return 1
		}
		return 0
	}
	var b *skills.Bundle
	if source != nil {
		dir, e := filepath.Abs(*source)
		if e != nil {
			fmt.Fprintln(stderr, "invalid skill source")
			return 2
		}
		if command == "build" {
			b, err = skills.Build(ctx, installed.Protocol(), *project, dir)
		} else {
			b, err = skills.Read(ctx, installed.Protocol(), dir)
		}
		if err == nil {
			err = b.Install(ctx, installed.Protocol(), store)
		}
	} else {
		b, err = skills.LoadInstalled(ctx, installed.Protocol(), store, *digest)
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
