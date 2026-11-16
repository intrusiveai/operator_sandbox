# Application model-response injection

Operator MUST consume the Interceptor OpenAPI bytes and identity recorded in
`internal/interceptor/openapi.json` and `internal/interceptor/contract.json`.
Adoption MUST compare the selected immutable source with the existing client,
capability and adapter checks. It MUST NOT infer compatibility from a campaign.

The integration MUST preserve the native `model_response` profile, its application
codec list and response-body placement root, including exact native hash ordering
and omitted fields. Public and compact target contexts MUST retain these facts.
Administrator routes MUST authorize an exact codec, scopes, placement operations,
pointers and merge fields through the existing resolver. An advertised surface
alone MUST NOT authorize an injection.

The shared execution schemas, Go/Python selected tool catalogs and normalization
MUST admit this surface with simulation mode, once/standing scope, model codec,
ordinal and optional observed-turn/original-request predicates. Existing input,
artifact, placement, ownership, lineage, budget and cleanup checks MUST apply.
Payloads MUST remain bounded JSON; intentionally invalid application response
shapes MUST NOT be passed through Harness provider-response validation. Existing
native arm/delete operations MUST carry the selector without a new dispatch path.

Deferred tool guidance MUST distinguish target application responses from Harness
responses and tool results sent to models. Recurring tool descriptions SHOULD
remain concise. The shared AsyncAPI identity MUST change with the interface;
matching Harness adoption MUST use the approved exact bytes in
`release/contract-lock.json`, not a new source-commit compatibility requirement.

Feedback MUST follow the model-response delivery rules in
`schemas/FEEDBACK_CONTRACT.md`. Mutation and response writing MUST remain distinct
from SDK acceptance and tool execution. Private response evidence MUST remain
subject to existing integrity and visibility rules. Original and delivered
response blobs and restored source journals MUST remain independently verifiable.

Verification MUST cover all advertised application codecs, invalid selectors and
scopes, exact route enforcement, normalizer parity, selected provider projections,
controlled native arm/invoke/read/delete, durable no-replay behavior, feedback
visibility and native original/restored evidence integrity. Synthetic fixtures MUST
identify their producing source and capture procedure. Controlled tests MUST NOT
be represented as live model or application-SDK qualification.
