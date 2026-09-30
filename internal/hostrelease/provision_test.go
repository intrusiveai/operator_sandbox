//go:build linux || darwin

package hostrelease

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type missingAccount struct{}

func (missingAccount) Error() string { return "no account" }
func (missingAccount) ExitCode() int { return 2 }
func TestLinuxProvisioningCreatesRestrictedAccountAndExplicitDockerGrant(t *testing.T) {
	for _, grant := range []bool{false, true} {
		calls := []string{}
		queries := 0
		dirs := []string{}
		deps := provisionDependencies{"linux", 0, func(_ context.Context, bin string, args ...string) ([]byte, error) {
			if args[0] == "passwd" {
				queries++
				if queries == 1 {
					return nil, missingAccount{}
				}
				return []byte("operator:x:991:991::/var/lib/operator:/usr/sbin/nologin\n"), nil
			}
			return []byte("docker:x:998:\n"), nil
		}, func(_ context.Context, bin string, args ...string) error {
			calls = append(calls, bin+" "+strings.Join(args, " "))
			return nil
		}, func(name string, uid, gid int) error {
			if uid != 991 || gid != 991 {
				return fmt.Errorf("bad identity")
			}
			dirs = append(dirs, name)
			return nil
		}, func() error { return nil }}
		result, err := provisionLinux(context.Background(), grant, grant, deps)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Created || result.UID != 991 || !reflect.DeepEqual(dirs, []string{"/opt/operator", "/etc/operator", "/var/lib/operator", "/run/operator"}) {
			t.Fatal(result, dirs)
		}
		all := strings.Join(calls, "\n")
		if strings.Contains(all, "usermod") != grant || !strings.Contains(all, "--shell /usr/sbin/nologin") || strings.Contains(all, "enable-linger operator") != grant || strings.Contains(all, "start user@991.service") != grant {
			t.Fatal(all)
		}
	}
}
func TestProvisionRejectsConflictingAccountWithoutChangingIt(t *testing.T) {
	d := provisionDependencies{"linux", 0, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("operator:x:1000:1000::/home/person:/bin/bash\n"), nil
	}, func(context.Context, string, ...string) error { t.Fatal("mutated conflicting account"); return nil }, func(string, int, int) error { t.Fatal("changed directories"); return nil }, func() error { t.Fatal("changed tmpfiles"); return nil }}
	if _, err := provisionLinux(context.Background(), true, false, d); err == nil {
		t.Fatal("accepted conflicting account")
	}
	d.goos = "darwin"
	if _, err := provisionLinux(context.Background(), false, false, d); err == nil {
		t.Fatal("provisioned macOS")
	}
}
