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
	"path/filepath"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

type targetFlags struct{ environment, profile, capabilities string }

func (s *targetFlags) bind(f *flag.FlagSet) {
	f.StringVar(&s.environment, "environment", "", "administrator environment directory")
	f.StringVar(&s.profile, "target-profile", "", "private TargetProfile file")
	f.StringVar(&s.capabilities, "capabilities", "", "public capability export with source companion")
}
func (s targetFlags) valid() bool {
	return s.environment == "" || (s.profile == "" && s.capabilities == "")
}

// Selection is explicit CLI input. Nothing in a scenario chooses a host path.
func (s targetFlags) resolve() (profile, public string, err error) {
	if !s.valid() {
		return "", "", errors.New("environment and explicit target selectors are exclusive")
	}
	if s.environment != "" {
		dir, e := filepath.Abs(s.environment)
		if e != nil {
			return "", "", e
		}
		info, e := os.Lstat(dir)
		if e != nil {
			return "", "", e
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode().Perm()&0022 != 0 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
			return "", "", hostconfig.ErrPrivate
		}
		return filepath.Join(dir, "target-profile.json"), filepath.Join(dir, "capabilities.json"), nil
	}
	if s.profile != "" {
		profile, err = filepath.Abs(s.profile)
		if err != nil {
			return "", "", err
		}
	}
	if s.capabilities != "" {
		public, err = filepath.Abs(s.capabilities)
	}
	return
}

func exportCapabilities(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	f := flag.NewFlagSet("capabilities export", flag.ContinueOnError)
	f.SetOutput(stderr)
	config := f.String("config", defaults.ConfigFile, "private installation configuration")
	output := f.String("output", "", "new public JSON file; parent must exist")
	native := f.String("native", "", "offline Interceptor capability JSON to project")
	var selection targetFlags
	selection.bind(f)
	if f.Parse(args) != nil || f.NArg() != 0 || *output == "" || *config == "" || !selection.valid() || (*native != "" && (selection.environment != "" || selection.capabilities != "")) {
		return 2
	}
	loaded, err := hostconfig.Load(*config, defaults)
	if err != nil {
		fmt.Fprintln(stderr, "cannot load configuration")
		return 1
	}
	c := loaded.Config.Contract
	installed, err := contractstore.LoadRuntime(ctx, c.Directory, contracts.PackageIdentity{Version: c.Version, Digest: c.Digest})
	if err != nil {
		fmt.Fprintln(stderr, "cannot verify installed contract")
		return 1
	}
	profileFile, public, err := selection.resolve()
	if err != nil {
		fmt.Fprintln(stderr, "invalid target selection")
		return 1
	}
	if profileFile == "" {
		profileFile = loaded.Config.Target.ProfileFile
	}
	profile, err := targetprofile.Load(profileFile)
	if err != nil {
		fmt.Fprintln(stderr, "cannot load private target profile")
		return 1
	}
	var exported *capabilities.Export
	if *native != "" && profile.HTTPS() != nil {
		fmt.Fprintln(stderr, "HTTPS capabilities are derived from the target profile")
		return 2
	}
	if *native != "" {
		name, e := filepath.Abs(*native)
		if e != nil {
			return 2
		}
		raw, e := staging.Capture(ctx, name, contracts.OrdinaryLimit)
		if e != nil {
			fmt.Fprintln(stderr, "cannot read native capability export")
			return 1
		}
		exported, err = capabilities.FromNative(installed.Protocol().Catalog(), raw, profile.Settings().TargetID)
	} else if public != "" {
		exported, err = capabilities.LoadExport(ctx, installed.Protocol().Catalog(), public, profile.Settings().TargetID)
	} else if profile.HTTPS() != nil {
		exported, err = capabilities.FromHTTPS(installed.Protocol().Catalog(), profile.HTTPS(), profile.Settings().TargetID)
	} else {
		fmt.Fprintln(stderr, "capability export requires --native, --capabilities or --environment")
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "capability verification failed")
		return 1
	}
	destination, err := filepath.Abs(*output)
	if err != nil {
		return 2
	}
	if err = exported.Save(ctx, destination); err != nil {
		fmt.Fprintln(stderr, "cannot publish capability export")
		return 1
	}
	if json.NewEncoder(stdout).Encode(map[string]string{"api_version": "operator.dev/capability-export-receipt/v1alpha1", "target_id": exported.TargetID(), "capability_source_digest": exported.SourceDigest(), "capability_projection_digest": exported.ProjectionDigest(), "public_digest": contracts.RawDigest(exported.PublicJSON()), "native_digest": contracts.RawDigest(exported.NativeJSON())}) != nil {
		return 1
	}
	return 0
}
