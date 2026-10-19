# Assessment and campaign finish

Harness submits `engine.finish` with a bounded assessment, a committed assessment
artifact receipt, or an explicit unavailable reason. Operator validates objective,
scenario, hypothesis, record, attempt and observation references; retains the
assessment with its own binding; closes execution admission; and starts existing
owned cleanup. No prior artifact publication or hypothesis/assessment registration
is required for an ordinary finish.

The assessment requires summary and objective outcomes. Optional collections
resolve to empty arrays and omitted status to partial. Supported outcomes require
execution evidence. Hypotheses may be local definitions in any order; historical
references remain available. Duplicate IDs, unknown references, cycles and invalid
ranges are rejected. See [the shared contract](../schemas/SIMPLE_CAMPAIGN_CONTRACT.md).

Acceptance returns a durable stop receipt and `finalization_status: pending`, with
the existing five-second Harness exit requirement. It does not claim cleanup
completed. Exact duplicate delivery returns the saved acknowledgement. Changed
content conflicts. A lost process, unknown outcome or interrupted finalization
never resumes or replays the campaign. Hard deadlines and administrative stop
remain independent of assessment generation.

Invalid assessments return CONCLUSION_INVALID with a safe rule and field path.
Harness-local errors and limit exhaustion use engine.diagnostic_report. Inspection,
reports, exported events and failed worker completion retain these causes. A
corrected submission may still finish normally; earlier rejections remain history.

For administrator examples, retained stopping diagnostics and separate
inspection/export, see [simple campaigns](SIMPLE_CAMPAIGNS.md). The exact breaking
package and separate Harness responsibilities are in the
[coordinated handoff](SIMPLE_CAMPAIGN_HANDOFF.md).
