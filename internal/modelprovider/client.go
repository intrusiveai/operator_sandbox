//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
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
	sign         func(context.Context, *http.Request, []byte) error
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
	if c == nil || ctx.Err() != nil {
		return nil, ErrRequest
	}
	value, err := contracts.Decode(raw, contracts.OrdinaryLimit)
	native, ok := value.(map[string]any)
	if err != nil || !ok {
		return nil, ErrRequest
	}
	if err = c.checkNative(native); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	header, prefix := "Authorization", "Bearer "
	var token, quota string
	if c.profile.UsesStoredCredential() || c.profile.Authentication == "api-key-env" {
		if c.profile.Authentication == "api-key-env" {
			token = os.Getenv(c.profile.APIKeyEnvironment)
		} else {
			resolved, err := c.resolver.Resolve(ctx, c.profile.CredentialID)
			if err != nil {
				return nil, ErrProvider
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
			return nil, ErrProvider
		}
	} else if c.bearer != nil {
		token, err = c.bearer(ctx)
		if err != nil {
			return nil, ErrProvider
		}
	}
	if (c.sign == nil && c.profile.Authentication != "none" && token == "") || len(token) > 1<<20 || strings.ContainsAny(token, "\x00\r\n") || ctx.Err() != nil {
		return nil, ErrProvider
	}
	// A non-rewindable reader plus disabled keepalive/redirects prevents net/http
	// from replaying a possibly sent request. No caller-supplied header is accepted.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.profile.Endpoint, io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		return nil, ErrRequest
	}
	req.ContentLength = int64(len(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	if c.sign != nil {
		if err := c.sign(ctx, req, raw); err != nil {
			return nil, ErrProvider
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
	response, err := c.http.Do(req)
	if err != nil {
		return nil, ErrProvider
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > c.profile.MaximumResponseBytes {
		return nil, ErrProvider
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	encoding := response.Header.Get("Content-Encoding")
	if err != nil || media != "application/json" || (encoding != "" && encoding != "identity") {
		return nil, ErrProvider
	}
	result, err := io.ReadAll(io.LimitReader(response.Body, c.profile.MaximumResponseBytes+1))
	if err != nil || int64(len(result)) > c.profile.MaximumResponseBytes || ctx.Err() != nil {
		return nil, ErrProvider
	}
	if resultValue, err := contracts.Decode(result, contracts.OrdinaryLimit); err != nil {
		return nil, ErrProvider
	} else if _, ok := resultValue.(map[string]any); !ok {
		return nil, ErrProvider
	}
	return result, nil
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
func awsSign(provider aws.CredentialsProvider, region string) func(context.Context, *http.Request, []byte) error {
	signer := v4.NewSigner()
	return func(ctx context.Context, request *http.Request, raw []byte) error {
		identity, err := provider.Retrieve(ctx)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		return signer.SignHTTP(ctx, identity, request, hex.EncodeToString(sum[:]), "bedrock", region, time.Now())
	}
}
