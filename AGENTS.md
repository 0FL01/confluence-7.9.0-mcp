# Confluence 7.9.0 MCP

Target: **only Confluence 7.9.0 (server, selfhosted)**. Data Center and Cloud are not compatibility targets.

## Map
- `main.go`: `run`/`main` bootstrap, `loadConfig` configuration, `setupServer` tool schemas, and `handle*` handlers.
- `main.go`: `ConfluenceClient.executeRequest`, `doRequest`, and `getJSON` own REST transport and response handling; `getImage` owns bounded attachment downloads.
- `main_test.go`: handler/client tests with `httptest`; `TestRun` injects a serve function without starting stdio.
- `.goreleaser.yaml`: six-platform builds, archives and checksums shared by CI snapshots and releases.
- `.github/workflows/`: CI tests/lint/snapshots and manual-only Release (new version on main); local builds use `go build`, no script.

## Rules
- Keep stdout reserved for MCP protocol traffic; diagnostics belong on stderr.
- When changing the public tool contract, update schemas in `setupServer`, handlers, tests, and README tool descriptions together.
- Handler validation/API failures use `mcp.NewToolResultError(...), nil`; successes contain response JSON as text except `confluence_get_content`, which prepends an inline-comment text block for `page`/`blogpost`, and `confluence_get_image`: native image + text for omitted/empty `downloadDir`, or text summary + file JSON array only when saving.
- Route REST calls through client helpers to retain request context, HTTP Basic authentication, and the 30-second timeout; close response bodies.
- `loadConfig` reads only cwd/.env without changing ENV; existing ENV wins per key, including empty values. Invalid/unreadable files fail without exposing input.
- Require non-empty `CONFLUENCE_LOGIN` and `CONFLUENCE_PASS`; reject colons in login, preserve password unchanged. Legacy `CONFLUENCE_API_TOKEN` is unused; no PAT fallback or cookies.
- `run` requires `/user/current` HTTP 200 with type `known` before MCP stdio; startup errors expose status/cause, never credentials or raw response bodies.
- Preserve URL environment precedence: `CONFLUENCE_BASE_URL`, then `CONFLUENCE_API_BASE_PATH`, then `CONFLUENCE_HOST`; retain context paths when adding `/rest/api`.
- Page bodies use Confluence `storage` representation, not Markdown.
- Updates fetch current content first, preserve type/space and unchanged title/body, and default version to current + 1; incomplete reused fields must fail before PUT, not become empty replacements.
- Get/update/image handlers reject IDs containing `/` or `..` before issuing requests; image IDs are escaped as one path segment.
- Image downloads stay on the configured origin/site context under `/download/attachments/...`, without redirects; cap actual bytes at 5 MiB and sniff PNG/JPEG/GIF/WebP without transforming bytes.
- Image saves require an explicit existing absolute `downloadDir` (native directory symlinks allowed); no directory creation, cwd fallback, or workspace sandbox. Use unique temp files (`0600` on Unix), clean partial failures, and leave successful files for caller deletion.
- MCP numeric arguments are currently decoded as `float64`; direct handler tests use that representation.

## Verify
Use Go 1.25.5 as specified by `go.mod` and CI; run commands from the repository root.
- `go vet ./...`
- `go test -v -race -coverprofile=coverage.out -covermode=atomic ./...`
- `go build -v -o confluence-mcp .` (Windows: `confluence-mcp.exe`; module/server identity stays unchanged).
- Packaging: `goreleaser check` then `goreleaser release --snapshot --clean` (no publication).
- Focused example: `go test -v -run '^TestHandleUpdateContent' .`
- Configuration tests isolate cwd and all `CONFLUENCE_*` configuration variables, including unused legacy `CONFLUENCE_API_TOKEN` (unset differs from empty); never use production credentials or parallel cwd/ENV tests.

## Docs
- `README.md`: current tool arguments, configuration, installation, and pinned Server 7.9.0 REST/CQL references.
