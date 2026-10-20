//go:build linux || darwin

package imagerelease

import (
	"bytes"
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"testing"
)

func requirements() Requirements {
	return Requirements{"1.10.0", contracts.PackageIdentity{Version: "0.1.0", Digest: contracts.RawDigest([]byte("package"))}, "darwin/arm64", "operator-container/v1"}
}
func release() Record {
	h := requirements()
	return Record{CompatibilityVersion, "1.9.0", h.Contract.Version, h.Contract.Digest, h.RuntimeProfile, "linux/arm64"}
}
func wire(r Record) []byte { raw, _ := json.Marshal(r); return raw }
func TestStableSemVerPrecedence(t *testing.T) {
	for _, v := range []struct {
		actual, minimum string
		ok              bool
	}{
		{"1.10.0", "1.9.0", true}, {"1.9.0", "1.10.0", false}, {"2.0.0", "1.999.999", true},
		{"1.0.0+host.01", "1.0.0+release.02", true}, {"1.0.0-rc.1", "0.1.0", false},
		{"2.0.0", "1.0.0-rc.1", false}, {"01.0.0", "1.0.0", false}, {"1.0", "1.0.0", false},
		{"9999999999999999999999.0.0", "9999999999999999999998.0.0", true},
		{"1.0.0+", "1.0.0", false}, {"1.0.0+bad+meta", "1.0.0", false},
	} {
		if got := atLeast(v.actual, v.minimum); got != v.ok {
			t.Fatal(v, got)
		}
	}
	for _, s := range []string{"0.1.0-alpha.1+build.01", "0.1.0+001", "0.1.0-x-y-z.--"} {
		if _, _, ok := version(s); !ok {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"0.1.0-01", "0.1.0-", "0.1.0-alpha..1", "v0.1.0", "0.1.0\n"} {
		if _, _, ok := version(s); ok {
			t.Fatal(s)
		}
	}
}

func TestStrictRecordAndCompatibility(t *testing.T) {
	r := release()
	raw := wire(r)
	for _, bad := range [][]byte{
		append([]byte(`{"platform":"linux/amd64",`), raw[1:]...), append(raw, []byte(` {}`)...),
		bytes.Replace(raw, []byte(`"platform":"linux/arm64"`), []byte(`"platform":null`), 1),
		append([]byte(`{"extra":false,`), raw[1:]...), []byte(`[]`), []byte("{\xff}"),
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatal("accepted invalid record", string(bad))
		}
	}
	for _, alter := range []func(*Record){
		func(r *Record) { r.MinimumOperatorVersion = "2.0.0" }, func(r *Record) { r.ContractPackageDigest = contracts.RawDigest([]byte("other")) },
		func(r *Record) { r.ContractPackageVersion = "0.1.1" }, func(r *Record) { r.Platform = "linux/amd64" },
		func(r *Record) { r.RuntimeProfile = "other" },
	} {
		r := release()
		alter(&r)
		if err := r.Compatible(requirements()); err == nil {
			t.Fatal(r)
		}
	}
	for _, alter := range []func(*Record){func(r *Record) { r.MinimumOperatorVersion = "1.0.0-rc.1" }, func(r *Record) { r.Platform = "darwin/arm64" }, func(r *Record) { r.ContractPackageVersion = "0.1.0-01" }} {
		r := release()
		alter(&r)
		if _, err := Parse(wire(r)); err == nil {
			t.Fatal(r)
		}
	}
}

func TestExplicitOriginPackageRejectsImplicitOriginImage(t *testing.T) {
	// Existing local compatibility matching is the sole admission mechanism.
	current := contracts.PackageIdentity{Version: "0.2.0", Digest: "sha256:057c4a53997b94bcd8ae18012daca4ec4610cd26abab9f32f748867362b422ee"}
	old := contracts.PackageIdentity{Version: "0.1.0", Digest: "sha256:a9e65f23eda2fe5f578a4da8a1128da45b7ee8f623cfd0a38066332877a61b0b"}
	host := requirements()
	host.OperatorVersion = "0.1.0"
	host.Contract = current
	record := release()
	record.MinimumOperatorVersion = "0.1.0"
	record.ContractPackageVersion = current.Version
	record.ContractPackageDigest = current.Digest
	if err := record.Compatible(host); err != nil {
		t.Fatal(err)
	}
	record.ContractPackageVersion = old.Version
	record.ContractPackageDigest = old.Digest
	if err := record.Compatible(host); err == nil {
		t.Fatal("implicit-origin image accepted")
	}
	record.ContractPackageVersion = current.Version
	if err := record.Compatible(host); err == nil {
		t.Fatal("relabeling old package bypassed digest match")
	}
	host.Contract = old
	record.ContractPackageVersion = current.Version
	record.ContractPackageDigest = current.Digest
	if err := record.Compatible(host); err == nil {
		t.Fatal("new image accepted by implicit-origin host")
	}
}
