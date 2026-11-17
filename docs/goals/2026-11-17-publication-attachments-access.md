# Goal: Publication attachments and access controls for Confluence 7.9.0

Status: active
Source: user publication specification (sections 1–6 and exclusions), approved RECON plan, and instruction to implement iteratively, commit/push, build and replace the executable in PATH.
Last updated: 2026-11-17

## Objective
Deliver four MCP tools for attachment upload, space permissions, page restrictions and granular restriction updates; document and test the safe publication sequence, publish the implementation and install it at the existing PATH destination.

## Execution Directive
Complete the frozen Required Outcomes using the listed Change Envelope and Primary Evidence. Work on the smallest unresolved outcome. Do not add requirements from reviews, tests, tools, speculative risks, or optional source text. Finish when every required outcome is resolved and affected constraints remain satisfied. Stop substantive work at a proven external blocker and record the smallest unlock.

## Frozen Contract

### Required Outcomes
- R1: `confluence_upload_attachment(contentId, filePath, fileName?, comment?, attachmentId?)`.
  - Source: specification §1 and upload acceptance in §6.
  - Acceptance: absolute readable regular local file streamed as multipart; PNG, Markdown, JSON and JSONL supported; default basename; create-only by default and explicit filename conflict; update only by explicit attachment ID after verifying its page; no blind retry after an ambiguous write. Return server attachment/page IDs, actual name, MIME, size, version and download link. Existing Basic Auth, no credential arguments.
  - Primary evidence: attachment handler/client httptest tests, including original bytes, conflict/no overwrite, membership and explicit update, cancellation/no replay.
  - Status: verified
  - Evidence: upload fixtures passed, including PNG/Markdown/JSON/JSONL byte preservation, >5 MiB streaming, conflicts without writes, explicit update/container checks, metadata readback and ambiguous/canceled write without replay. Covered by full Go 1.25.5 race gate; live write acceptance is separately tracked in R6.
- R2: `confluence_get_space(spaceKey, includePermissions?)`.
  - Source: specification §2.
  - Acceptance: space ID/key/name/type/homepage, caller permissions if available, user/group assignments, honest anonymous-access status, completeness and warnings; distinguish empty, forbidden, unsupported, unrequested and unknown/error. Never infer deny-all or employees-only from absent fields; no nested-group effective-permission engine.
  - Primary evidence: space REST and legacy JSON-RPC fixtures plus read-only installed-tool smoke.
  - Status: in_progress
  - Evidence: fixtures passed for context-preserving Basic Auth, metadata-only mode, assignments/special subjects, empty/forbidden/unsupported/missing results and no raw RPC error disclosure. Installed-tool smoke remains after delivery.
- R3: `confluence_get_restrictions(contentId, includeAncestors?)`.
  - Source: specification §3.
  - Acceptance: own read/update subjects, fully paginated users/groups, separately sourced ancestor read restrictions with page IDs/titles, completeness and warnings. No claim that empty own ACL means public; no inherited update interpretation.
  - Primary evidence: restrictions httptest reproducing multiple pages without next links, separate ancestor/source and inaccessible-data behavior.
  - Status: verified
  - Evidence: fixtures passed for no-next-link pagination, unequal effective user/group page limits, separately sourced ancestor reads and incomplete/forbidden data. No arbitrary pagination truncation is reported as complete.
- R4: `confluence_update_restrictions(contentId, operation, addUsers?, removeUsers?, addGroups?, removeGroups?, dryRun?, confirmRemoveLastRestriction?)`.
  - Source: specification §4.
  - Acceptance: read/update only; user keys and group names; dryRun=true by default and no writes; granular changes preserve unrelated subjects/operation; explicit confirmation before predicted last removal; readback after writing; partial/unknown outcomes never generic success. No space permission writes.
  - Primary evidence: handler fixtures for dry run, granular preservation, confirmation, partial execution and readback.
  - Status: verified
  - Evidence: fixtures passed for dry run/no writes, granular changes preserving other subjects/operation, last-removal confirmation/current-state guard, partial/unknown outcomes without replay, incomplete readback and lost read access. External write acceptance remains R6.
- R5: Publication safety and current documentation.
  - Source: specification §5, §6 and approved plan.
  - Acceptance: schemas/tests/README describe four new tools and inspect-space/parent → empty page → set/verify read ACL → sensitive body/attachments → readback sequence. No credentials, cookies, Authorization headers or local authorization-file contents in responses/logs. Understandable safe errors; existing tool contracts preserved.
  - Primary evidence: transport error fixtures, schema checks and final documentation/diff review.
  - Status: verified
  - Evidence: ten-tool schemas/defaults, safe HTTP/RPC error fixtures, no redirect follow/replay, README publication order and API limitations reviewed. AGENTS map updated without unrelated changes. Full race gate and vet passed; lint passed after test-only constant/switch fixes.
- R6: External write and rendering acceptance in an agreed location.
  - Source: specification §6 and explicit external-write boundary.
  - Acceptance: on an agreed test page, upload PNG/JSONL and read metadata, reject duplicate without changing existing version, explicitly update to a new version, embed ri:attachment and check rendering in an authorized browser, verify ACL preservation/dry run/readback. Never use the ML homepage or publish the real case.
  - Primary evidence: agreed-page integration observations and authorized-browser visual check.
  - Status: blocked
  - Evidence: no test parent/page or test ACL subjects have been approved. RECON performed read-only operations only. Implementation/local verification can proceed; external writes cannot.
- R7: Commit and push intended implementation and documentation.
  - Source: latest user instruction.
  - Acceptance: intended changes committed and pushed to origin/main, excluding pre-existing user .gitignore changes, credentials and generated files.
  - Primary evidence: commit/push and matching remote SHA; required CI results where applicable.
  - Status: pending
  - Evidence:
- R8: Build and replace the existing executable in PATH.
  - Source: latest user instruction.
  - Acceptance: Go 1.25.5 build from published implementation installed atomically at /home/stfu/.local/bin/confluence-mcp, executable, matching build bytes; installed MCP schemas and read-only new tools verified without exposing private results.
  - Primary evidence: build metadata/checksum and installed-binary smoke.
  - Status: pending
  - Evidence:

### Constraints
- Confluence Server exactly 7.9.0; no Cloud/DC compatibility assumptions. Preserve Basic Auth, context paths, request cancellation, 30-second HTTP timeout and stdout reserved for MCP.
- Existing .env is private and must not be edited, copied into artifacts, printed, or committed. Private responses remain in memory; probes print only non-sensitive structure/status.
- No external mutations without an agreed test destination and subjects. User authorized code publication and local binary replacement, not publication of MARKETING-449 or changes to space rights.
- No automatic write replay or redirect following. Safe error handling must not emit raw server error bodies.
- Existing image-download 5 MiB policy is not an attachment-upload size limit. Do not invent the server's configured upload limit or misclassify its ambiguous 404 responses.
- Granular restriction API lacks a demonstrated transaction/CAS: read-before-write and readback do not guarantee atomicity against another administrator. State this limitation; do not claim a local lock solves it.
- RPC special-subject encoding is unverified: never equate empty userName/groupName automatically with anonymous; report unknown/incomplete rather than infer employee-only access.

### Non-goals
- No space creation/global permission/group-membership management, Markdown converter, case-specific publisher, browser authentication, search/read redesign, new dependencies/services/cache/persistent state, or CI/release redesign.

## Change Envelope
- main.go transport helpers and setupServer registrations; new attachments.go, space.go, restrictions.go in existing package main; adjacent Go tests and existing schema/transport consumers.
- README.md, compact AGENTS.md map/rules update if needed, and this goal. No changes to historic goals, user .gitignore/.env, go.mod/dependencies or workflows unless an explicit existing gate proves a blocker.
- Temporary build/smoke artifacts only under approved external cache; binary replacement only at discovered existing PATH executable.

## Approved Plan Copy
1. Upload plus necessary transport support: stream multipart field file and optional comment with X-Atlassian-Token: nocheck; precheck filename and explicitly send allowDuplicated=false; verify attachment container before explicit /data update; server metadata/readback; sanitize errors, preserve authentication/context/timeout, disable write redirects/retries and report ambiguous outcomes.
2. Read access: REST space metadata plus fixed read-only JSON-RPC getSpacePermissionSets/getPermissions using existing Basic Auth; independently classified data availability and honest anonymous uncertainty. REST own read/update restrictions, users/groups pagination until both exhausted even without _links.next; ancestors carry only inherited read restrictions and source metadata.
3. Update restrictions: full preflight/read and dry-run diff; stable user keys/group names; reject contradictory deltas; granular experimental PUT/DELETE only, additions before removals; explicit last-removal confirmation; stop on failure and return per-action confirmed/failed/unknown/not-attempted state plus actual readback. Preserve unrelated subjects/operation and document concurrency limitations.
- Existing create can make a semantically empty page using <p></p>; do not weaken its nonempty-content validation.
- Register/document all four tools, test direct contracts with httptest, run full Go race tests and vet for the shared transport surface, then commit/push/build/install and read-only installed-tool smoke. External acceptance stays pending until explicitly authorized.

## RECON Evidence
- Live manifest confirmed 7.9.0; Basic Auth /user/current returned known.
- REST /space/{key}?expand=homepage,permissions,operations returns metadata/homepage but omits permission/operation fields. REST/experimental space permission routes tested were 404 and absent from WADL.
- Read-only JSON-RPC getSpacePermissionSets and getPermissions returned HTTP 200 with result, no RPC error. Assignment DTO fields: type, userName, groupName.
- Own restrictions and ancestors GETs work. limit=1 at starts 0 and 1 yielded distinct users; start=2 was empty, all without _links.next.
- Live WADL/OPTIONS confirm attachment POST and allowDuplicated and experimental singular user/group PUT/DELETE routes. No write permissions or effects have been tested.
- Pinned docs: https://docs.atlassian.com/ConfluenceServer/rest/7.9.0/ ; https://developer.atlassian.com/server/confluence/confluence-json-rpc-apis/ ; https://docs.atlassian.com/ConfluenceServer/javadoc/7.9.0/com/atlassian/confluence/rpc/soap/ConfluenceSoapService.html ; https://docs.atlassian.com/ConfluenceServer/javadoc/7.9.0/com/atlassian/confluence/plugins/restapi/experimental/resources/ExperimentalContentRestrictionsResource.html
- SpacePermissionSubjectType 7.9.0 includes USER, GROUP, ANONYMOUS and ALL_AUTHENTICATED_USERS; legacy RPC special-subject serialization remains unproven.

## Current Checkpoint
- Closes: R2 installed smoke and R7/R8 delivery.
- Smallest next action: commit/push the reviewed files, build published source with Go 1.25.5, atomically install and run read-only MCP smoke.
- Expected evidence: matching remote SHA, clean-source build metadata/checksum, ten tools and read-only space/restriction/dry-run results from installed binary.
- Stop or replan if: delivery needs overwriting unrelated work, or external acceptance requires unapproved writes.

## Current State
- Resolved: R1/R3/R4/R5 locally; R2 local fixtures passed.
- Last relevant evidence: GOTOOLCHAIN=go1.25.5 go vet ./...; full go test -race -covermode=atomic ./... passed (coverage artifact outside repo); golangci-lint run ./... passed with zero issues; focused access tests passed after test-only lint fixes; git diff --check clean.
- Blocker: R6 needs agreed test location/subjects and authorized-browser validation.
- Next: commit/push/build/install and read-only smoke; do not mark the overall goal complete while R6 remains blocked.

## Material Decisions
- 2026-11-17: user approved implementation, commit/push and replacement of current PATH binary. Existing unrelated .gitignore modification excluded.
- 2026-11-17: use only narrow built-in legacy RPC reads and experimental granular ACL writes proven present in 7.9.0; no Cloud endpoint substitution.

## Checkpoint History
- 2026-11-17: frozen original specification and approved plan after successful read-only RECON; external write acceptance explicitly blocked, not waived.
- 2026-11-17: implemented four tools without dependencies; reviewed runtime/contract diff. Full Go 1.25.5 race tests and vet passed. Existing CI lint found test-only style issues, corrected; lint and affected tests now pass. No private server mutations.

## Completion
- Not complete: implementation/delivery pending and external acceptance requires authorization.
