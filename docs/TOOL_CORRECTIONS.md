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
alternatives describe their distinguishing required fields. Unknown, unsafe field
names are omitted while preserving the nearest safe parent path.

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
approved schema bytes. It never copies Harness-supplied diagnostic prose into
trusted summaries. Invalid internal resolution data is a product error; the model
must not supply hidden campaign identities or canonical receipt fields to fix it.
Unavailable capabilities and exhausted allowances cannot be repaired by repeating
requests or changing arguments to bypass policy.

`no-progress-limit` means consecutive turns without recognized progress reached
the configured limit. The report identifies both counts, assessment availability,
and the preceding rejection separately. Accepted finish with an unavailable
assessment remains unsuccessful even when Harness exits zero. Worker completion,
inspection, exported evidence and Markdown reports retain these stopping facts.

Contract 0.7.1 adds schema-rule references and no-progress reporting. Deploy a
matching Harness pinned to the version and exact document hash in
`release/contract-lock.json`; no source-commit equality is required. Existing
campaign evidence is not rewritten and failed campaigns never resume.

Invalid-call exhaustion uses the existing `invalid-call-limit` condition and
`invalid_call_limit` reason. Harness reports stage `termination`, actual observed
and configured counts, and `resubmission_safe: false`. Operator displays the limit
as the terminal cause and earlier field corrections as separate history. It never
promotes an unavailable assessment to success because Harness exited zero. These
implementation rules use the existing contract 0.7.1 fields and identity.
