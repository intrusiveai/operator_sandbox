package interceptor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestContractDiscovery(t *testing.T) {
	identity := func(version, hash string) string {
		b, _ := json.Marshal(ContractIdentity{version, hash})
		return string(b)
	}

	for _, tc := range []struct {
		name, body, reason string
		status             int
	}{
		{"match", identity(SupportedContractVersion, SupportedContractSHA256), "", 200},
		{"mismatch", identity("9.0.0", SupportedContractSHA256), "version_mismatch", 200},
		{"hash-mismatch", identity(SupportedContractVersion, strings.Repeat("a", 64)), "hash_mismatch", 200},
		{"hash-missing", `{"interceptor_contract_version":"0.2.0"}`, "invalid_hash", 200},
		{"hash-malformed", identity(SupportedContractVersion, "SECRET"), "invalid_hash", 200},
		{"hash-prefix", identity(SupportedContractVersion, "sha256:"+SupportedContractSHA256), "invalid_hash", 200},
		{"hash-case", identity(SupportedContractVersion, strings.ToUpper(SupportedContractSHA256)), "invalid_hash", 200},
		{"missing", `{}`, "invalid_version", 200},
		{"null", `{"interceptor_contract_version":null}`, "invalid_version", 200},
		{"number", `{"interceptor_contract_version":1}`, "invalid_version", 200},
		{"syntax", `{"interceptor_contract_version":"v0.1.0"}`, "invalid_version", 200},
		{"secret", `{"interceptor_contract_version":"SECRET-value"}`, "invalid_version", 200},
		{"duplicate", `{"interceptor_contract_version":"0.1.0","interceptor_contract_version":"0.1.0"}`, "invalid_response", 200},
		{"404", `{"code":"not_found"}`, "endpoint_unavailable", 404},
		{"503", `{"code":"unavailable"}`, "http_rejection", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if string(raw) != "{}" || r.URL.String() != Address+"/v1/contract-version" || r.Method != "POST" || r.GetBody != nil {
					t.Fatal("wrong discovery request")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":` + strconv.Itoa(tc.status) + `,"session_revision":0,"body":` + tc.body + `}`))}, nil
			})}}
			err := c.CheckContract(context.Background())
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *CompatibilityError
			if !errors.As(err, &failure) || failure.Reason != tc.reason || failure.Expected != SupportedContractVersion || calls != 1 || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("%+v calls=%d", err, calls)
			}
			if tc.reason == "version_mismatch" && failure.Actual != "9.0.0" {
				t.Fatal(failure)
			}
		})
	}
}

func TestContractTransportFailures(t *testing.T) {
	for _, mode := range []string{"connection", "timeout", "cancel", "non-json", "oversized", "truncated", "trailing", "status-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			calls := 0
			c := &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if mode == "connection" {
					return nil, errors.New("SECRET transport details")
				}
				if mode == "timeout" {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				rpl := wireResponse(t, 200, ContractIdentity{SupportedContractVersion, SupportedContractSHA256})
				switch mode {
				case "non-json":
					rpl.Header.Set("Content-Type", "text/html")
					rpl.StatusCode = 404
				case "oversized":
					rpl.ContentLength = JSONLimit + 1
				case "truncated":
					rpl.Body = io.NopCloser(strings.NewReader(`{"status":200`))
				case "trailing":
					rpl.Body = io.NopCloser(strings.NewReader(`{"status":200,"session_revision":0,"body":{}} {}`))
				case "status-mismatch":
					rpl.StatusCode = 500
				}
				return rpl, nil
			})}}
			err := c.CheckContract(ctx)
			var failure *CompatibilityError
			want := "invalid_response"
			switch mode {
			case "connection":
				want = "transport_failure"
			case "timeout":
				want = "timeout"
			case "cancel":
				want = "canceled"
			case "non-json":
				want = "endpoint_unavailable"
			}
			if !errors.As(err, &failure) || failure.Reason != want || strings.Contains(err.Error(), "SECRET") || calls > 1 {
				t.Fatalf("%+v", err)
			}
		})
	}
}

func TestContractVersionSyntax(t *testing.T) {
	for _, v := range []string{"", "v0.1.0", "01.1.0", "0.1.0-rc", "0.1.0 ", strings.Repeat("1", 129) + ".0.0"} {
		if ValidContractVersion(v) {
			t.Fatal(v)
		}
	}
	if !ValidContractVersion(SupportedContractVersion) {
		t.Fatal("invalid compiled pin")
	}
}
