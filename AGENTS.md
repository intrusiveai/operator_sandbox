# Repository instructions

## Interceptor qualification dependency updates

`release/interceptor-qualification.json` identifies the exact published Interceptor
build used by Operator's release qualification. Its source commit, archive URL,
archive SHA-256 and integration contract version must describe the same build.
The pin does not restrict ordinary campaigns to that source commit; runtime
compatibility uses the supported Interceptor contract version.

For coordinated Operator and Interceptor changes:

1. Keep the Operator PR **draft** while the required Interceptor changes or their
   candidate publication are pending. Link the dependency PR and state the blocker.
2. Wait for the Interceptor changes to merge and for publication of that exact
   source revision to succeed. A branch head, merged PR or running release workflow
   is not evidence that the required archive has been published.
3. Retrieve the published release metadata, archive and checksum file. Verify the
   actual archive digest against the checksum file, release metadata and GitHub
   asset digest. Verify the packaged source revision, platform, contract version
   and executable checksum. Review changes since the previous dependency pin.
4. Update and commit the complete dependency identity together. Do not copy a
   source commit from a checkout and assume it identifies the published archive.
   Do not invent a future checksum or relax verification to accept changed bytes.
5. Run the affected qualification/pin tests. Recheck the published identity before
   marking the Operator PR ready or recommending its merge. If the artifact has
   changed, repeat verification and update the pin first. Never present a PR with
   a known pending pin refresh as ready to merge.

Rolling candidate releases can replace an archive at the same URL. A later
Interceptor rebuild invalidates an earlier checksum pin even when its contract
version is unchanged. If that happens, return the Operator PR to draft until the
dependency update is complete. Rerunning an old Operator workflow retains its old
committed pin; a corrected pin requires a new candidate source and qualification.

This sequence does not authorize changes, merges or publication in another
repository. Respect the user's repository scope and report external blockers.
Use the releases skill and
[candidate release guide](.github/maintainers/CANDIDATE_RELEASES.md) for the
existing publication and qualification procedures. Do not claim successful
candidate qualification from metadata checks or local tests alone.
