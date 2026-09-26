// Native digest field order/omission rules from Interceptor capability.go at
// 567e6e0546a8f797c1673f90ba749e907b3dc56f. Keep changes covered by native fixtures.
package capabilities

import "github.com/intrusiveai/operator_sandbox/internal/nativedelivery"

type FeedbackCapability struct {
	ViewVersion       string   `json:"view_version"`
	SelectionModes    []string `json:"selection_modes"`
	Kinds             []string `json:"kinds"`
	MaximumChunkBytes int      `json:"maximum_chunk_bytes"`
	MaximumEntries    int      `json:"maximum_entries"`
}

type nativeManifest struct {
	Feedback              FeedbackCapability        `json:"feedback"`
	DeliverySchemaProfile string                    `json:"delivery_schema_profile,omitempty"`
	APIVersion            string                    `json:"api_version"`
	Kind                  string                    `json:"kind"`
	InterceptorRelease    string                    `json:"interceptor_release"`
	EnvironmentDigest     string                    `json:"environment_digest"`
	ApplicationDigest     string                    `json:"application_digest"`
	Target                TargetCapability          `json:"target"`
	FeedbackProfiles      []string                  `json:"feedback_profiles"`
	Operations            []OperationCapability     `json:"operations"`
	CrossVMOperations     []string                  `json:"cross_vm_operations"`
	InjectionSurfaces     []string                  `json:"injection_surfaces"`
	InjectionProfiles     []InjectionProfile        `json:"injection_profiles"`
	Services              []ServiceCapability       `json:"services"`
	FileNamespaces        []FileNamespaceCapability `json:"file_namespaces,omitempty"`
	Oracles               []string                  `json:"oracle_types"`
	ModelProviders        []ModelProviderCapability `json:"model_providers"`
	Network               NetworkCapability         `json:"network"`
	SnapshotCapable       bool                      `json:"snapshot_capable"`
	Limits                CapabilityLimits          `json:"limits"`
	Digest                string                    `json:"digest"`
}
type TargetCapability struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Interface string `json:"interface"`
}
type OperationCapability struct {
	Delivery          *nativedelivery.Contract `json:"delivery,omitempty"`
	DeliveryStatus    string                   `json:"delivery_status,omitempty"`
	MaximumInputBytes int64                    `json:"maximum_input_bytes,omitempty"`
	ID                string                   `json:"id"`
	Method            string                   `json:"method,omitempty"`
	Path              string                   `json:"path,omitempty"`
}
type InjectionProfile struct {
	Surface        string   `json:"surface"`
	Scopes         []string `json:"scopes"`
	Placements     []string `json:"placements"`
	SelectorFields []string `json:"selector_fields"`
	Carriers       []string `json:"carriers"`
}
type ServiceCapability struct {
	Implementation       string                       `json:"implementation,omitempty"`
	Endpoints            []nativedelivery.Endpoint    `json:"endpoints,omitempty"`
	ID                   string                       `json:"id"`
	Kind                 string                       `json:"kind"`
	Role                 string                       `json:"role"`
	MCP                  bool                         `json:"mcp,omitempty"`
	ResponseMode         string                       `json:"response_mode,omitempty"`
	Transports           []string                     `json:"transports"`
	InjectionSurfaces    []string                     `json:"injection_surfaces,omitempty"`
	CreatableCollections []ResourceCreationCapability `json:"creatable_collections,omitempty"`
}
type ResourceCreationCapability struct {
	Collection   string `json:"collection"`
	MaxResources int    `json:"max_resources"`
}
type FileNamespaceCapability struct {
	ID           string `json:"id"`
	AllowCreate  bool   `json:"allow_create"`
	MaxFiles     int    `json:"max_files,omitempty"`
	MaxFileBytes int64  `json:"max_file_bytes,omitempty"`
}
type ModelProviderCapability struct {
	Kind                string   `json:"kind"`
	Codecs              []string `json:"codecs"`
	AuthenticationModes []string `json:"authentication_modes"`
	ModelSelection      string   `json:"model_selection"`
	Text                bool     `json:"text"`
	ClientFunctions     bool     `json:"client_functions"`
	StructuredOutput    bool     `json:"structured_output"`
	Streaming           bool     `json:"streaming"`
	Embeddings          bool     `json:"embeddings"`
	Multimodal          bool     `json:"multimodal"`
	ProviderHostedTools bool     `json:"provider_hosted_tools"`
	ProviderStoredState bool     `json:"provider_stored_state"`
}
type NetworkCapability struct {
	DNS   DNSCapability `json:"dns"`
	HTTP  bool          `json:"http_frontdoor"`
	HTTPS TLSCapability `json:"https_frontdoor"`
}
type DNSCapability struct {
	Enabled        bool     `json:"enabled"`
	RecordTypes    []string `json:"record_types,omitempty"`
	UnknownPolicy  string   `json:"unknown_policy,omitempty"`
	Upstream       bool     `json:"upstream_forwarding"`
	MaximumQueries int64    `json:"maximum_queries,omitempty"`
}
type TLSCapability struct {
	Enabled        bool   `json:"enabled"`
	TrustMode      string `json:"trust_mode,omitempty"`
	MinimumVersion string `json:"minimum_version,omitempty"`
}
type CapabilityLimits struct {
	MaximumAttempts      int   `json:"maximum_attempts"`
	MaximumArtifacts     int   `json:"maximum_artifacts"`
	MaximumArtifactBytes int64 `json:"maximum_artifact_bytes"`
	MaximumInvocations   int64 `json:"maximum_invocations"`
	MaximumSnapshots     int64 `json:"maximum_snapshots"`
}
