package preparation_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

func endpointProfile(t *testing.T, in *preparation.Input, srv *httptest.Server) {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	s := in.Profile.Settings()
	s.Interceptor = &interceptor.Connection{Protocol: u.Scheme, Address: u.Hostname(), Port: port}
	if cert := srv.Certificate(); cert != nil {
		s.Interceptor.CACertificatesPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	}
	var err error
	in.Profile, err = targetprofile.Parse(encode(s))
	if err != nil {
		t.Fatal(err)
	}
}

// These are native API fixtures over the production client, not a live
// Interceptor or Docker deployment. Recovery has no current profile to consult.
func TestRecoveryUsesRetainedEndpoint(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "attachment-http", true: "prepared-https"}[prepared], func(t *testing.T) {
			var native *peer
			var calls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var response interceptor.Response
				var err error
				switch r.URL.Path {
				case "/v1/contract-version":
					response = interceptor.Response{Status: 200, Body: encode(interceptor.ContractIdentity{Version: interceptor.SupportedContractVersion, SHA256: interceptor.SupportedContractSHA256})}
				case "/v1/status":
					status, e := native.Status(r.Context(), "campaign-1")
					status.EvidenceMaxBytes = 4 << 30
					err = e
					response = interceptor.Response{Status: 200, Body: encode(status), SessionRevision: native.revision}
				case "/v1/operations":
					var wire struct {
						Request interceptor.OperationRequest `json:"request"`
						Body    json.RawMessage              `json:"body"`
					}
					err = json.NewDecoder(r.Body).Decode(&wire)
					if err == nil {
						var q interceptor.PreparedOperation
						q, err = interceptor.PrepareOperation(wire.Request, wire.Body)
						if err == nil {
							response, err = native.Execute(r.Context(), q)
						}
					}
				default:
					t.Error("unexpected native path", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				status := response.Status
				if status == 204 {
					status = 200
				}
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(response)
			})
			start := httptest.NewServer
			if prepared {
				start = httptest.NewTLSServer
			}
			srv := start(handler)
			defer srv.Close()
			in := fixture(t)
			endpointProfile(t, &in, srv)
			native = &peer{input: in, revision: 5}
			root := t.TempDir()
			os.Chmod(root, 0700)
			if prepared {
				target, err := preparation.Build(in)
				if err != nil {
					t.Fatal(err)
				}
				writer, launch, savedRoot := preparedLaunchConfigured(t, target, nil, "")
				root = savedRoot
				if _, err = target.Persist(writer, launch.EngineContext); err != nil {
					t.Fatal(err)
				}
				if err = writer.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				intent := campaign.AttachmentIntent{Interceptor: in.Profile.Settings().Interceptor, CampaignID: "campaign-1", LaunchID: "launch-1", StartRequestID: strings.Repeat("a", 32), WorkerInstanceID: "worker-1", InputsFingerprint: contracts.RawDigest([]byte("inputs"))}
				writer, err := campaign.CreateAttachment(root, intent)
				if err != nil {
					t.Fatal(err)
				}
				if err = writer.Bind(in.Attachment); err != nil {
					t.Fatal(err)
				}
				if err = writer.Verify(in.Status); err != nil {
					t.Fatal(err)
				}
				if err = writer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			recover := func() (nativerecovery.Outcome, error) {
				if prepared {
					return nativerecovery.Run(context.Background(), root, "campaign-1", true, nil)
				}
				return nativerecovery.RunAttachment(context.Background(), root, "campaign-1", nil)
			}
			out, err := recover()
			if err != nil || out.State != "complete" || out.Closure != "confirmed" {
				t.Fatal(out, err)
			}
			before := calls.Load()
			if before < 3 {
				t.Fatal("recovery did not use the saved endpoint", before)
			}
			again, err := recover()
			if err != nil || again.State != "complete" || calls.Load() != before {
				t.Fatal("replayed recovery", again, err, calls.Load())
			}
		})
	}
}

func TestLateEvidenceUsesRetainedEndpoint(t *testing.T) {
	var native *latePeer
	var exports atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/evidence" {
			exports.Add(1)
			data := native.export.data
			w.Header().Set("Content-Type", "application/x-tar")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("X-Content-Sha256", contracts.RawDigest(data))
			w.Header().Set("X-Evidence-Max-Bytes", strconv.FormatInt(4<<30, 10))
			w.Write(data)
			return
		}
		response := interceptor.Response{Status: 200}
		switch r.URL.Path {
		case "/v1/contract-version":
			response.Body = encode(interceptor.ContractIdentity{Version: interceptor.SupportedContractVersion, SHA256: interceptor.SupportedContractSHA256})
		case "/v1/status":
			status, err := native.Status(r.Context(), "campaign-1")
			if err != nil {
				t.Error(err)
			}
			response.Body = encode(status)
		default:
			t.Error("unexpected request", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	root, writer, p, _, _ := lateFixture(t, func(in *preparation.Input) { endpointProfile(t, in, srv) })
	native = p
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{}, nil)
	if err != nil || !report.Complete() || exports.Load() != 1 {
		t.Fatal(report, err, exports.Load())
	}
	again, err := nativerecovery.CollectEvidence(context.Background(), root, "campaign-1", &lateDocker{}, nil)
	if err != nil || !again.Complete() || exports.Load() != 1 {
		t.Fatal("repeated export", again, err, exports.Load())
	}
}
