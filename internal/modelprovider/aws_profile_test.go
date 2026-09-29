//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAWSProfileBindingAndPublicProjection(t *testing.T) {
	base := profile(t, "bedrock-converse", "").Settings()
	base.Authentication = "aws-profile"
	base.AWSProfile = "private-admin-profile"
	parse := func(s Settings) (*Profile, error) { raw, _ := json.Marshal(s); return Parse(raw) }
	p, err := parse(base)
	if err != nil {
		t.Fatal(err)
	}
	public, err := p.PublicModel([]byte("[]"))
	if err != nil || bytes.Contains(public, []byte(base.AWSProfile)) || bytes.Contains(public, []byte("aws_profile")) {
		t.Fatal("private AWS profile exposed")
	}
	changed := base
	changed.AWSProfile = "other-profile"
	other, err := parse(changed)
	if err != nil || other.Digest() == p.Digest() {
		t.Fatal("profile not digest-bound")
	}
	for _, change := range []func(*Settings){func(s *Settings) { s.AWSProfile = "" }, func(s *Settings) { s.Authentication = "workload-identity" }, func(s *Settings) { s.CredentialID = "unexpected" }, func(s *Settings) { s.Provider = "azure-openai" }, func(s *Settings) { s.AWSProfile = "bad\nname" }} {
		s := base
		change(&s)
		if _, err = parse(s); err == nil {
			t.Fatal("accepted inconsistent authentication")
		}
	}
}
