//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"runtime"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

type diagnosticCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Action string `json:"action,omitempty"`
}

// doctor never resolves secrets, attaches a target, starts a service, creates a
// container or repairs installation state. Results contain fixed codes only.
func doctor(ctx context.Context, args []string, stdout, stderr io.Writer, defaults hostconfig.Paths) int {
	f := flag.NewFlagSet("doctor", flag.ContinueOnError)
	f.SetOutput(stderr)
	config := f.String("config", defaults.ConfigFile, "private installation configuration")
	offline := f.Bool("offline", false, "skip read-only local Docker inspection")
	if f.Parse(args) != nil || f.NArg() != 0 || *config == "" {
		return 2
	}
	checks := []diagnosticCheck{}
	failed := false
	add := func(name string, err error, action string) {
		item := diagnosticCheck{Name: name, Status: "ok"}
		if err != nil {
			failed = true
			item.Status = "failed"
			item.Action = action
		}
		checks = append(checks, item)
	}
	loaded, err := hostconfig.Load(*config, defaults)
	add("configuration", err, "check_private_configuration")
	if err == nil {
		c := loaded.Config
		_, err = contractstore.Load(ctx, c.Contract.Directory, contracts.PackageIdentity{Version: c.Contract.Version, Digest: c.Contract.Digest})
		add("installed_contract", err, "install_matching_contract_package")
		if c.Target.ProfileFile == "" {
			checks = append(checks, diagnosticCheck{Name: "target_profile", Status: "not_checked", Action: "select_explicit_submission_target"})
		} else {
			_, err = targetprofile.Load(c.Target.ProfileFile)
			add("target_profile", err, "check_private_target_profile")
		}
		model, modelErr := modelprovider.Load(c.Model.ProfileFile)
		add("model_profile", modelErr, "check_private_model_profile")
		var cc credentials.Config
		if c.Credentials.File != "" {
			cc, err = credentials.Load(c.Credentials.File)
		} else {
			err = nil
		}
		if err == nil && modelErr == nil && model.Settings().Authentication == "secret-store" {
			found := false
			for _, ref := range cc.Credentials {
				if ref.CredentialID == model.Settings().CredentialID {
					found = true
				}
			}
			if !found {
				err = modelprovider.ErrProfile
			}
		}
		add("credential_configuration", err, "check_private_credential_references")
		info, e := os.Lstat(c.State.Root)
		if e == nil {
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
				e = hostconfig.ErrPrivate
			}
		}
		add("state_directory", e, "provision_private_state_directory")
		_, e = dockercontrol.ImagePlatform(runtime.GOOS + "/" + runtime.GOARCH)
		add("host_platform", e, "use_supported_host_platform")
		if *offline {
			checks = append(checks, diagnosticCheck{Name: "local_image", Status: "not_checked"})
		} else {
			docker, e := dockercontrol.New(c.Docker.Executable)
			if e == nil {
				_, e = docker.ResolveImage(ctx, c.Docker.Endpoint, c.Engine.Image, runtime.GOOS+"/"+runtime.GOARCH)
			}
			add("local_image", e, "check_docker_endpoint_and_install_native_image")
		}
	}
	// Installation checks cannot qualify production execution or authentication.
	checks = append(checks, diagnosticCheck{Name: "live_provider_target_and_runtime_qualification", Status: "not_checked"})
	status := "checks_passed"
	if failed {
		status = "failed"
	}
	if json.NewEncoder(stdout).Encode(struct {
		APIVersion string            `json:"api_version"`
		Status     string            `json:"status"`
		Checks     []diagnosticCheck `json:"checks"`
	}{"operator.dev/doctor/v1alpha1", status, checks}) != nil || failed {
		return 1
	}
	return 0
}
