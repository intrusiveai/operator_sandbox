# HTTPS seam capability capture

`https-seams-native.json` retains the native manifest produced by the published
Interceptor CLI at source `5311ad5797d2b594993a5bea02d5aae71e724800`, integration
contract `0.2.0`. Its source archive was
`interceptor_sandbox-v0.1.4-darwin_arm64.tar.gz`, SHA-256
`d23baaeece5909448ef3fbec057faf15b8150bf3f865bec2f27b5b7c23a3ea5b`.

The capture came from `scripts/qualify_interceptor_release.py --local` with
Operator source `063f6446cc6785e324fc6848aa361e67e6564b38` and the Harness image
pinned in `release/interceptor-qualification.json`. The target's fixed application
code calls a declared HTTPS seam to a local TLS fixture; Harness invokes the
application but cannot select or configure the seam. The campaign verified
compatibility, attachment, one invocation and one seam request, observation
reading, accepted finish, exit zero, cleanup and evidence export on macOS ARM64
with Docker Desktop 29.8.2. This is controlled local verification, not cloud
qualification or Linux candidate-promotion evidence.

The manifest is JSON-indented by the qualifier without changing its native field
values or digest. Keep it as an independent serialization oracle; do not regenerate
its expected digest using Operator. Environment/image hashes change across fixture
builds, so a new capture must record its producing Interceptor archive and source.
No origins, credentials or private seam diagnostics are included.
