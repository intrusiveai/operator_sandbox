//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
)

type Resolver interface {
	Resolve(context.Context, string) (credentials.Resolution, error)
}
type Client struct {
	profile      Settings
	resolver     Resolver
	http         *http.Client
	sign         func(context.Context, *http.Request, []byte) ([]string, error)
	bearer       func(context.Context) (string, error)
	googleBearer func(context.Context) (token, quota string, err error)
}

func New(ctx context.Context, profile *Profile, resolver Resolver) (*Client, error) {
	if profile == nil || ctx.Err() != nil {
		return nil, ErrProfile
	}
	s := profile.Settings()
	if s.UsesStoredCredential() && resolver == nil {
		return nil, ErrProfile
	}
	c := &Client{profile: s, resolver: resolver, http: boundedHTTP()}
	if s.CACertificatesPEM != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(s.CACertificatesPEM)) {
			return nil, ErrProfile
		}
		c.http.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
	}
	if s.Provider == "bedrock-converse" {
		cfg, err := credentials.AWSIdentityConfig(ctx, s.Region, s.AWSProfile, s.Authentication)
		if err != nil {
			return nil, ErrProfile
		}
		endpoint, err := bedrockruntime.NewDefaultEndpointResolverV2().ResolveEndpoint(ctx, bedrockruntime.EndpointParameters{Region: &s.Region})
		if err != nil {
			return nil, ErrProfile
		}
		destination := endpoint.URI
		destination.Path = "/model/" + s.Model + "/converse"
		destination.RawPath = "/model/" + url.PathEscape(s.Model) + "/converse"
		c.profile.Endpoint = destination.String()
		c.sign = awsSign(cfg.Credentials, s.Region)

	} else if !s.UsesStoredCredential() && s.Authentication != "api-key-env" && s.Authentication != "none" {
		switch s.Provider {
		case "azure-openai":
			identity, err := credentials.AzureIdentity(s.Authentication)
			if err != nil {
				return nil, ErrProfile
			}
			c.bearer = func(ctx context.Context) (string, error) {
				token, err := identity.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://cognitiveservices.azure.com/.default"}})
				return token.Token, err
			}
		case "vertex-gemini":
			c.googleBearer = func(ctx context.Context) (string, string, error) {
				identity, err := credentials.GoogleIdentity(ctx, s.Authentication)
				if err != nil {
					return "", "", err
				}
				quota, err := credentials.GoogleQuotaProject(identity)
				if err != nil {
					return "", "", err
				}
				token, err := identity.TokenSource.Token()
				if err != nil {
					return "", "", err
				}
				return token.AccessToken, quota, nil
			}
		default:
			return nil, ErrProfile
		}
	}
	return c, nil
}

func boundedHTTP() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (c *Client) Close() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}

// Generate accepts a native body already validated against the shared codec and
// frozen prompt/tools policy. It returns exact native HTTP response bytes,
// never credentials or transport errors.
func (c *Client) Generate(ctx context.Context, raw []byte) ([]byte, error) {
	response, _, err := c.GenerateWithMetadata(ctx, raw)
	return response, err
}

// GenerateWithMetadata returns only selected response facts, including on HTTP
// success so subsequent contract-validation failures can retain their provenance.
func (c *Client) GenerateWithMetadata(ctx context.Context, raw []byte) (body []byte, observed Diagnostic, callErr error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	// Defer order matters: classify the observed failure before our own cleanup
	// cancels the context, so cleanup cannot turn a rejection into cancellation.
	defer cancel()
	category, reason := "request_validation", "request_invalid"
	if c != nil {
		observed.Provider, observed.Model = c.profile.Provider, c.profile.Model
	}
	defer func() {
		if callErr != nil {
			if category != "http_rejection" {
				category = failureCategory(ctx, callErr, category)
				switch category {
				case "timeout":
					reason = "deadline_exceeded"
				case "cancellation":
					reason = "canceled"
				}
			}
			observed = observed.WithFailure(category, reason)
			failure := FailureError(observed)
			// Preserve only fixed sentinel causes, never URL/SDK/transport strings.
			if errors.Is(callErr, ErrRequest) {
				failure = &Failure{observed, ErrRequest}
			}
			callErr = failure
			body = nil
		}
	}()
	if c == nil || ctx.Err() != nil {
		return nil, observed, ErrRequest
	}
	value, err := contracts.Decode(raw, contracts.OrdinaryLimit)
	native, ok := value.(map[string]any)
	if err != nil || !ok {
		return nil, observed, ErrRequest
	}
	if err = c.checkNative(native); err != nil {
		return nil, observed, err
	}

	category, reason = "credential_preparation", "credential_unavailable"
	header, prefix := "Authorization", "Bearer "
	var token, quota string
	if c.profile.UsesStoredCredential() || c.profile.Authentication == "api-key-env" {
		if c.profile.Authentication == "api-key-env" {
			token = os.Getenv(c.profile.APIKeyEnvironment)
		} else {
			resolved, err := c.resolver.Resolve(ctx, c.profile.CredentialID)
			if err != nil {
				return nil, observed, err
			}
			token = resolved.Value
		}
		switch c.profile.Provider {
		case "anthropic-messages":
			if c.profile.Authentication != "workload-token" {
				header, prefix = "X-Api-Key", ""
			}
		case "gemini-api", "vertex-gemini":
			header, prefix = "X-Goog-Api-Key", ""
		case "azure-openai":
			header, prefix = "Api-Key", ""
		}
	} else if c.googleBearer != nil {
		token, quota, err = c.googleBearer(ctx)
		if err != nil {
			return nil, observed, err
		}
	} else if c.bearer != nil {
		token, err = c.bearer(ctx)
		if err != nil {
			return nil, observed, err
		}
	}
	if (c.sign == nil && c.profile.Authentication != "none" && token == "") || len(token) > 1<<20 || strings.ContainsAny(token, "\x00\r\n") || ctx.Err() != nil {
		return nil, observed, ErrProvider
	}
	// A non-rewindable reader plus disabled keepalive/redirects prevents net/http
	// from replaying a possibly sent request. No caller-supplied header is accepted.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.profile.Endpoint, io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		return nil, observed, ErrRequest
	}
	req.ContentLength = int64(len(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	var signedSecrets []string
	if c.sign != nil {
		signedSecrets, err = c.sign(ctx, req, raw)
		if err != nil {
			return nil, observed, ErrProvider
		}
	} else if token != "" {
		req.Header.Set(header, prefix+token)
	}
	if quota != "" {
		req.Header.Set("X-Goog-User-Project", quota)
	}
	if c.profile.Provider == "anthropic-messages" {
		req.Header.Set("Anthropic-Version", c.profile.APIVersionHeader)
	}
	category, reason = "connection", "no_response"
	response, err := c.http.Do(req)
	if err != nil {
		return nil, observed, err
	}
	defer response.Body.Close()
	secrets := diagnosticSecrets(req, token, signedSecrets)
	observed = responseMetadata(c.profile.Provider, c.profile.Model, response, secrets)
	if response.StatusCode != http.StatusOK {
		category, reason = "http_rejection", "http_rejected"
		observed.readErrorDetails(response, c.profile.MaximumResponseBytes, secrets)
		// The observed rejection remains primary even if reading its optional error
		// body times out or fails. Do not recategorize it as a no-response failure.
		observed = observed.WithFailure(category, reason)
		return nil, observed, FailureError(observed)
	}
	category, reason = "response_validation", "response_too_large"
	if response.ContentLength > c.profile.MaximumResponseBytes {
		return nil, observed, ErrProvider
	}
	category, reason = "response_validation", "invalid_media_type"
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	encoding := response.Header.Get("Content-Encoding")
	if err != nil || media != "application/json" || (encoding != "" && encoding != "identity") {
		return nil, observed, ErrProvider
	}
	category, reason = "response_read", "read_failed"
	result, err := io.ReadAll(io.LimitReader(response.Body, c.profile.MaximumResponseBytes+1))
	if err != nil {
		return nil, observed, err
	}
	if ctx.Err() != nil {
		return nil, observed, ctx.Err()
	}
	category, reason = "response_validation", "response_too_large"
	if int64(len(result)) > c.profile.MaximumResponseBytes {
		return nil, observed, ErrProvider
	}
	category, reason = "response_validation", "invalid_json"
	if resultValue, err := contracts.Decode(result, contracts.OrdinaryLimit); err != nil {
		return nil, observed, ErrProvider
	} else if _, ok := resultValue.(map[string]any); !ok {
		return nil, observed, ErrProvider
	}
	return result, observed, nil
}

func (c *Client) checkNative(m map[string]any) error {
	for _, key := range []string{"headers", "endpoint", "url", "region", "project", "deployment", "api_key", "previous_response_id", "conversation", "background"} {
		if _, ok := m[key]; ok {
			return ErrRequest
		}
	}
	var maximum any
	switch c.profile.Codec {
	case "openai-chat-text-tools-v1":
		if m["model"] != c.profile.Model || m["stream"] != false || m["store"] != false {
			return ErrRequest
		}
		maximum = m["max_completion_tokens"]
	case "openai-responses-text-tools-v1":
		if m["model"] != c.profile.Model || m["stream"] != false || m["store"] != false {
			return ErrRequest
		}
		maximum = m["max_output_tokens"]
	case "anthropic-messages-text-tools-v1":
		if m["model"] != c.profile.Model || m["stream"] != false {
			return ErrRequest
		}
		maximum = m["max_tokens"]
	case "gemini-text-tools-v1":
		if _, ok := m["model"]; ok {
			return ErrRequest
		}
		config, ok := m["generationConfig"].(map[string]any)
		if !ok {
			return ErrRequest
		}
		maximum = config["maxOutputTokens"]
	case "bedrock-converse-text-tools-v1":
		config, ok := m["inferenceConfig"].(map[string]any)
		if !ok {
			return ErrRequest
		}
		maximum = config["maxTokens"]
	default:
		return ErrRequest
	}
	number, ok := maximum.(json.Number)
	if !ok {
		return ErrRequest
	}
	count, err := number.Float64()
	if err != nil || count < 1 || count > float64(c.profile.MaximumCompletionTokens) || count != float64(int64(count)) {
		return ErrRequest
	}
	return nil
}

// AWS signing uses the SDK workload credential chain and signer, while the HTTP
// transport preserves exact native bytes and performs no SDK generation retries.
func awsSign(provider aws.CredentialsProvider, region string) func(context.Context, *http.Request, []byte) ([]string, error) {
	signer := v4.NewSigner()
	return func(ctx context.Context, request *http.Request, raw []byte) ([]string, error) {
		identity, err := provider.Retrieve(ctx)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		err = signer.SignHTTP(ctx, identity, request, hex.EncodeToString(sum[:]), "bedrock", region, time.Now())
		_, signature, _ := strings.Cut(request.Header.Get("Authorization"), "Signature=")
		return []string{identity.AccessKeyID, identity.SecretAccessKey, identity.SessionToken, signature}, err
	}
}
