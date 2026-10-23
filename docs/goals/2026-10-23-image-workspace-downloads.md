# Goal: View and save Confluence image attachments

Status: active
Source: approved Basic Auth/image/workspace plan; user requested implementation, documentation, commit/push, build and installation at ~/.local/bin/confluence-mcp.
Last updated: 2026-10-23

## Objective
Publish and install a minimal Confluence Server 7.9.0 MCP change that reads image attachments inline or saves them into an explicit workspace directory using login/password, without PAT.

## Execution Directive
Complete the frozen Required Outcomes using the listed Change Envelope and Primary Evidence. Work on the smallest unresolved outcome. Do not add requirements from reviews, tests, tools, speculative risks, or optional source text. Finish when every required outcome is resolved and affected constraints remain satisfied.

## Frozen Contract
- R1: Basic Auth and one image tool with optional workspace download.
  - Source: approved plan, including the Jira-style explicit-directory pattern.
  - Acceptance: required CONFLUENCE_LOGIN/CONFLUENCE_PASS replace PAT; existing configuration/bootstrap invariants remain. confluence_get_image(attachmentId, downloadDir?) returns a native image without disk writes by default, or saves original bytes uniquely in an existing absolute directory and returns text-only path/MIME/size. Bounded 5 MiB PNG/JPEG/GIF/WebP download, same origin/site context, no redirects; no overwrites, 0600 on Unix, partial-write cleanup.
  - Primary evidence: focused existing configuration/schema/transport tests and image handler HTTP/filesystem fixtures, followed by the existing Go verification gates and live read-only image smoke.
  - Status: verified
  - Evidence: Go 1.25.5 vet and full race/coverage tests passed (97.3% statements); golangci-lint reported zero issues. HTTP/filesystem fixtures verify both result modes and affected safety/configuration invariants. Pre-implementation Basic Auth smoke returned HTTP 200 for current user, authorized page, image metadata and image bytes; PNG, 10,630 bytes, no PAT/cookies/disk storage. No credentials or private content recorded.
- R2: Update current documentation and publish the reviewed implementation.
  - Source: user requested minimal diff, documentation, commit and push.
  - Acceptance: README/tool schemas/AGENTS match actual auth and both result modes; intended source/docs only committed and pushed to origin/main.
  - Primary evidence: final diff review, commit/push output and matching remote SHA.
  - Status: in_progress
  - Evidence: README, schemas and AGENTS updated together; publication follows final diff check.
- R3: Build and install ~/.local/bin/confluence-mcp.
  - Source: latest explicit user instruction.
  - Acceptance: Go 1.25.5 binary built from published implementation, installed with matching bytes and executable permissions, and verified through MCP image/save calls.
  - Primary evidence: build metadata, checksum comparison and installed-binary smoke.
  - Status: pending
  - Evidence:

### Constraints and Non-goals
- Preserve the existing five tool arguments/raw JSON results, module/server identity, dependencies, URL aliases/context, timeout, request context and update-preservation behavior.
- User-owned .env stays private and unmodified; never print credentials. Tests use isolated synthetic configuration.
- Images only, not arbitrary attachments. No directory creation, workspace sandbox/discovery, cache, cleanup worker, image conversion, cookies/PAT fallback, retries or CI/release redesign.
- Follow Jira's explicit-directory save pattern, not its SDK or extra image-dimension policy. Successful files remain for caller deletion.

## Change Envelope
- main.go: configuration, authentication, getImage, handler and setupServer; main_test.go: direct fixtures/consumers and regression evidence.
- README.md and surgical AGENTS.md contract updates; this execution record.
- Temporary smoke/build artifacts stay in the approved external cache; installed executable only at the user-requested destination. No dependency or workflow changes.

## Current Checkpoint / State
- R1 implemented and verified; R2 publication and R3 build/install/installed-MCP smoke are next.
- Changes are limited to the four approved runtime/test/current-documentation files and this record; historical goal documents are unchanged.
- No external blocker remains. After publishing, build from a clean tree, install atomically, verify both MCP modes against the authorized page, view the saved image locally and delete smoke artifacts.

## Checkpoint History
- 2026-10-23: scope frozen; user supplied missing login privately; GET-only Basic Auth smoke succeeded without PAT, cookies or file storage.
- 2026-10-23: R1 passed focused tests, full vet/race/coverage and existing CI lint; no new dependencies, interfaces or tool beyond the approved image operation.

## Completion
- Pending implementation, publication and installation evidence.
