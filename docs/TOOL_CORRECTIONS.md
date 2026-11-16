# Correcting rejected tool calls

A rejected call identifies the model-facing field, the failed rule, and the next
valid action. `execution_occurred` and `resubmission_safe` distinguish a correctable
pre-execution rejection from uncertain effects or a terminal product failure.
Correct the identified field while keeping valid parts of the request. Use a new
call identity for corrected arguments; never replay completed target actions to
repair an assessment. An unchanged duplicate call returns its recorded result.

Schema corrections use the actual installed constraints: types, required/unknown
fields, enum/constant values, string lengths/patterns, numeric bounds, array
sizes/uniqueness, object counts and conditional alternatives. Discriminators such
as record kind and injection surface select the applicable alternative. Ambiguous
alternatives describe their distinguishing required fields. Unsupported-field
diagnostics identify the containing object and quote the actual
unsupported names as data. Supported fields, their purposes and required flags
come from that object's applicable schema, including permitted patterned fields.
There is no global list of recognized field names. Names that exceed the bound
or contain credential-like or unsafe characters are explicitly omitted with a reason.

For example, `claims[0].uncertainties[0]` contains a **label**, not explanatory
prose. Declare the uncertainty and reference that label:

```json
{
  "uncertainties": [{
    "label": "limited-feedback",
    "kind": "unavailable-feedback",
    "description": "Internal operation-error feedback was unavailable."
  }],
  "claims": [{
    "statement": "The inspected evidence supports a limited conclusion.",
    "interpretation": "inconclusive",
    "assurance": "refund-behavior",
    "confidence": "low",
    "uncertainties": ["limited-feedback"]
  }]
}
```

This is an assessment fragment, not a complete finish request. `tool-guide:finish`
contains the minimal complete request and permitted values. Execution handles go
in `attempts`, historical record handles in `records`, and inspected citation
handles in `observations`. Readable observation handles remain available for
pagination; follow returned `continuation.arguments` and cite only inspected
ranges. Hypotheses can be declared directly without preregistration.

Diagnostic `constraint` fields contain an installed schema identifier, JSON
Pointer and validation keyword. Operator reconstructs the explanation from its
approved schema bytes. Producer explanations and corrections are retained after
credential filtering and
size bounds, attributed to Harness rather than asserted as host observations.
Operator does not reinterpret a reason through a reason-to-message table. Unknown
but syntactically valid reasons remain identifiable. Invalid internal resolution
data is a product error; the model
must not supply hidden campaign identities or canonical receipt fields to fix it.
Unavailable capabilities and exhausted allowances cannot be repaired by repeating
requests or changing arguments to bypass policy.

`no-progress-limit` means consecutive turns without recognized progress reached
the configured limit. The report identifies both counts, assessment availability,
and the preceding rejection separately. Accepted finish with an unavailable
assessment remains unsuccessful even when Harness exits zero. Worker completion,
inspection, exported evidence and Markdown reports retain these stopping facts.

The contract includes schema-rule references and no-progress reporting. Deploy a
matching Harness pinned to the version and exact document hash in
`release/contract-lock.json`; no source-commit equality is required. Existing
campaign evidence is not rewritten and failed campaigns never resume.

Invalid-call exhaustion uses the existing `invalid-call-limit` condition and
`invalid_call_limit` reason. Harness reports stage `termination`, actual observed
and configured counts, and `resubmission_safe: false`. Operator displays the limit
as the terminal cause and earlier field corrections as separate history. It never
promotes an unavailable assessment to success because Harness exited zero. These
implementation rules use the existing diagnostic fields; the current approved
identity is in `release/contract-lock.json`.

## Retained failure details

The short error envelope remains bounded independently of the full correction.
`correction` permits 4096 bytes. Unsupported-field details include up to eight
quoted names of at most 64 UTF-8 bytes, omission counts/reasons and the containing
object path. Full schema purposes are available through `tool-guide:<name>`;
compact recurring provider declarations avoid duplicating advanced setup prose.
If a correction cannot include all supported fields, it says how many were omitted
and directs the caller to guidance. No field values are copied into schema errors.

`facts` carries at most 16 producer-selected scalar facts, such as expected handle
category, requested bytes, available bytes or the permitted offset. Values are
bounded, credential-filtered data; they cannot grant permissions. Safe producer
message and correction are both preserved, and omissions are explicit. Available failure facts determine the recovery action instead of a general
reason identifier.

An oversized evidence read or artifact may be corrected to fit remaining capacity.
Exhausted object/request allowances and forbidden observation categories instead
require using existing permitted evidence or reporting a gap. Database injection
errors identify the failed selector, scope, placement, key or row rule. None of
these rejections dispatches or replays a target invocation.

Model-profile checks identify a configuration field and the rule to satisfy without
printing endpoint credentials or private values. Credential failures retain typed
backend codes, observed HTTP status and categories where available. Unknown causes
remain unknown: raw SDK error messages are never used to guess permission failures.
