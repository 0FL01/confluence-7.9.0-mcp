# Confluence MCP

Give your AI agent six tools to read, search, create, and update Confluence content, find spaces, and retrieve image attachments. A single Go binary runs locally over MCP stdio and calls your Confluence REST API.

**Supported version: Confluence Server 7.9.0, self-hosted only.** Data Center and Cloud are not compatibility targets.

| Tool | What it does |
| --- | --- |
| `confluence_get_content` | Retrieve content by ID, including its storage body and inline comment threads |
| `confluence_search_content` | Search content and spaces using CQL |
| `confluence_create_content` | Create a page or blog post with a storage-format body |
| `confluence_update_content` | Update content while preserving unchanged fields |
| `confluence_list_spaces` | List spaces or search their titles |
| `confluence_get_image` | Return an image attachment or save it to an explicitly chosen server directory |

## Quick start

### 1. Prepare a private `.env`

Use your **Confluence Server 7.9.0 login and password**. All requests use HTTP Basic authentication; personal access tokens and cookies are not used. Use HTTPS in production: Basic authentication does not encrypt credentials by itself.

Create `.env` in a private configuration directory, for example `/absolute/path/to/private/confluence-config/.env`:

```dotenv
CONFLUENCE_BASE_URL='https://confluence.example.invalid/confluence'
CONFLUENCE_LOGIN='replace-with-your-login'
CONFLUENCE_PASS='replace-with-your-password'
```

Replace the example URL, login, and password. Include your Confluence context path if applicable. Keep the file out of version control and restrict access, for example on Linux or macOS:

```bash
chmod 600 /absolute/path/to/private/confluence-config/.env
```

The server reads `.env` from its **working directory**. Inherited environment variables override the same keys in the file, **including empty values**; an empty inherited login or password causes startup to fail.

### 2. Install `confluence-mcp`

No release has been published under the [canonical repository](https://github.com/0FL01/confluence-7.9.0-go-mcp) yet. Build from source with **Go 1.25.5**, as specified by `go.mod` and CI:

```bash
git clone https://github.com/0FL01/confluence-7.9.0-go-mcp.git
cd confluence-7.9.0-go-mcp
go build -o confluence-mcp .
```

On Windows, use `go build -o confluence-mcp.exe .`. On Linux or macOS, you can install the binary into `~/.local/bin`:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 confluence-mcp "$HOME/.local/bin/confluence-mcp"
```

After the first manual release, you can use the prebuilt archives described below.

### 3. Configure OpenCode

Merge this into your project or global `opencode.jsonc`, preserving other settings:

```json
{
  "mcp": {
    "confluence": {
      "type": "local",
      "command": ["/absolute/path/confluence-mcp"],
      "cwd": "/absolute/path/to/private/confluence-config",
      "timeout": 35000,
      "enabled": true
    }
  }
}
```

Replace both paths with real absolute paths; `cwd` must contain your `.env`. For example, the command can be `/home/your-user/.local/bin/confluence-mcp` after installation. The server loads the file itself. Keep credentials in the private `.env`; inherited `CONFLUENCE_*` values must be unset if the file should supply those keys.

### 4. Verify startup and read content

Restart OpenCode after configuring the server, then check it from the same workspace:

```bash
opencode mcp list
```

Before starting MCP stdio, the server checks `/rest/api/user/current` and requires HTTP `200` with `type: "known"`. Once connected, ask OpenCode to call `confluence_get_content` with the `contentId` of a page your account can access. This also verifies that account's permission to read the page.

If startup fails, check `cwd`, `.env` readability and syntax, inherited environment overrides, the Confluence URL, and the login and password. Diagnostics go to stderr; stdout is reserved for MCP protocol traffic. See the configuration details below for the full startup contract.

<details>
<summary>Prebuilt archives and checksum verification</summary>

Once a release is available, download the archive for your platform and `checksums.txt` from the [canonical Releases page](https://github.com/0FL01/confluence-7.9.0-go-mcp/releases).

| Platform | Archive |
| --- | --- |
| Linux amd64 | `confluence-mcp_linux_amd64.tar.gz` |
| Linux arm64 | `confluence-mcp_linux_arm64.tar.gz` |
| macOS amd64 | `confluence-mcp_darwin_amd64.tar.gz` |
| macOS arm64 | `confluence-mcp_darwin_arm64.tar.gz` |
| Windows amd64 | `confluence-mcp_windows_amd64.zip` |
| Windows arm64 | `confluence-mcp_windows_arm64.zip` |

All six archives contain the native `confluence-mcp` binary (`confluence-mcp.exe` on Windows), `README.md`, and `LICENSE`. Binaries are built with `CGO_ENABLED=0`; `checksums.txt` contains SHA-256 hashes.

Verify the selected archive before extracting it. For Linux amd64:

```bash
sha256sum confluence-mcp_linux_amd64.tar.gz
```

Compare the printed hash with the entry for **that exact filename** in the downloaded `checksums.txt`. On macOS, use `shasum -a 256` with your selected archive instead. After the hash matches, extract and install; for Linux amd64:

```bash
tar -xzf confluence-mcp_linux_amd64.tar.gz
mkdir -p "$HOME/.local/bin"
install -m 755 confluence-mcp "$HOME/.local/bin/confluence-mcp"
```

On Windows, compute the SHA-256 hash in PowerShell, compare it with the matching `checksums.txt` entry, then extract the ZIP. For Windows amd64:

```powershell
Get-FileHash .\confluence-mcp_windows_amd64.zip -Algorithm SHA256
```

After the hash matches:

```powershell
Expand-Archive .\confluence-mcp_windows_amd64.zip -DestinationPath .\confluence-mcp
```

Use the extracted binary's absolute path as the MCP command.

</details>

<details>
<summary>Configuration, authentication, and other MCP clients</summary>

### Configuration

`CONFLUENCE_LOGIN` and `CONFLUENCE_PASS` are required and must be non-empty. The login must not contain a colon (`:`); the password is used unchanged, without trimming. All requests to Confluence, including image downloads, use **HTTP Basic authentication** with these credentials. The legacy `CONFLUENCE_API_TOKEN` is unused: there is no PAT fallback or cookie authentication.

### `.env` and inherited environment

The server reads only `.env` in its current working directory using `godotenv.Read` from `github.com/joho/godotenv` v1.5.1. It does not search parent directories, the executable directory, or `.env.local`. A missing `.env` permits environment-only configuration; a present but unreadable or malformed file fails startup safely.

File values and the inherited process environment are merged without mutating the process environment. An inherited environment variable overrides the **same key** from `.env`, including when its value is empty. An empty inherited `CONFLUENCE_PASS`, for example, overrides a file password and fails credential validation.

Set at least one URL variable. After merging, the first non-empty value wins in this order:

| Priority | Variable | Purpose |
| --- | --- | --- |
| 1 | `CONFLUENCE_BASE_URL` | Confluence instance URL, including any context path |
| 2 | `CONFLUENCE_API_BASE_PATH` | Alternative URL, which may already end in `/rest/api` |
| 3 | `CONFLUENCE_HOST` | Alternative hostname or URL |

Prefer setting only `CONFLUENCE_BASE_URL`. URL aliases retain their priority after merging: a `CONFLUENCE_BASE_URL` from `.env` takes priority over an inherited `CONFLUENCE_HOST`, because those are different keys.

URLs must have a hostname, use `http` or `https`, and contain no query string or fragment. Values without a scheme default to HTTPS. The server preserves the context path and appends `/rest/api` unless the URL path already ends in `/rest/api`.

| Configured value | REST root |
| --- | --- |
| `confluence.example.com` | `https://confluence.example.com/rest/api` |
| `https://confluence.example.com/confluence` | `https://confluence.example.com/confluence/rest/api` |
| `https://confluence.example.com/confluence/rest/api` | `https://confluence.example.com/confluence/rest/api` |

As an optional alternative to `.env`, set the inherited environment when running from the repository root:

```bash
export CONFLUENCE_LOGIN='replace-with-your-login'
export CONFLUENCE_PASS='replace-with-your-password'
export CONFLUENCE_BASE_URL='https://confluence.example.invalid/confluence'
go run .
```

Clients that use the `mcpServers` configuration format, such as Claude Desktop, can also supply environment variables directly. This client-specific format differs from the OpenCode example above:

```json
{
  "mcpServers": {
    "confluence": {
      "command": "/absolute/path/confluence-mcp",
      "env": {
        "CONFLUENCE_LOGIN": "replace-with-your-login",
        "CONFLUENCE_PASS": "replace-with-your-password",
        "CONFLUENCE_BASE_URL": "https://confluence.example.invalid/confluence"
      }
    }
  }
}
```

### Authenticated startup

After configuration and credential validation, the server sends `GET /rest/api/user/current` under the configured Confluence context path before starting MCP stdio. It requires HTTP `200` and a JSON user object with `type: "known"`. This request uses the client's 30-second timeout and is not retried.

Invalid configuration, an unreadable or malformed `.env`, anonymous users, HTML or invalid JSON responses, non-200 API responses (including `400`, `401`, and `403`), and network failures cause startup to exit with status `1`. Startup diagnostics go to stderr, omit secrets, and never include raw server response bodies; stdout remains reserved for MCP protocol traffic.

This check proves authentication as a known user. Access to individual content and spaces still depends on that user's permissions.

</details>

<details>
<summary>Tool arguments and REST contracts</summary>

### Tool contract

The six tool IDs and their argument names are listed below. Successful calls return the **Confluence REST response JSON as MCP text**. `confluence_get_content` adds an `inlineComments` field for pages/blogposts by default and returns one JSON text block, without a prose preamble. `confluence_search_content` can omit three service fields with `compact: true`; its default response is unchanged. `confluence_get_image` instead returns native MCP image content with text, or a text-only saved-file result, as described below. Validation and API failures return an MCP tool-error result, except comment-loading failures described below. Request context is retained, and the HTTP client has a 30-second timeout.

Optional numeric arguments must be finite whole numbers in the REST signed 32-bit integer range. `limit` and `start` accept `0` through `2147483647`; `version` accepts `1` through `2147483647`. Fractional and out-of-range values are rejected. An omitted `limit` defaults to `25`; an explicit `limit: 0` is sent unchanged to Confluence and does not carry a count-only guarantee. Pagination applies only to search and space listing.

### `confluence_get_content`

Retrieve content with `GET /content/{contentId}` relative to the configured REST root.

**Arguments:**
- `contentId` (string, required): Non-empty content ID; IDs containing `/` or `..` are rejected before a request.
- `expand` (string, optional): Comma-separated content expansions, such as `space,version`.
- `inlineComments` (string, optional): `summary` (default), `full`, or `none`. Invalid values are rejected before any request.

`body.storage` is always included in the expansions, together with any caller-supplied expansions. This request has no pagination parameters.

For `page` and `blogpost` content (unless `inlineComments: "none"`), the tool fetches comments with `GET /content/{contentId}/child/comment?location=inline&location=resolved&depth=all&expand=body.storage,history,version,extensions.inlineProperties,extensions.resolution&limit=100&start=N`, following pagination until `_links.next` is absent. Resolved threads and replies are included; **footer comments are not included**, explicitly indicated by `footerCommentsIncluded: false`. A separate `location=footer&depth=all&limit=100&start=N` query, without expansions and with the same pagination, counts the excluded footer comments and replies in `footerComments`. This is not all page discussion. `summary` reduces MCP output, not the REST data fetched.

The single JSON result retains the native page fields (`id`, `body`, etc.) and adds an `inlineComments` object:

```json
{
  "mode": "summary",
  "footerCommentsIncluded": false,
  "footerComments": 6,
  "grouping": "originalSelection-and-response-order (heuristic)",
  "counts": {"threads": 2, "comments": 3},
  "threads": [
    {"root": {"id": "9"}, "replies": []},
    {"root": {"id": "10"}, "replies": [{"id": "11"}]}
  ]
}
```

The example abbreviates comment fields. Read this discussion context before relying on the page body:

- `summary`: every root and reply includes `id`, `author`, `createdDate`, `version`, and `bodyStorage` — the complete, unchanged `body.storage.value`, including all XHTML and newlines, with no truncation. This replaces the former `bodyPreview` field. Only roots include `originalSelection`, `markerRef`, and `status`, which describes the thread; replies omit these fields because their anchors are empty and their REST resolution can remain `open` even in a resolved thread. `updatedBy` is included only when it differs from `author`; otherwise its value is implicitly `author`. Independently, `updatedDate` is included only when it differs from `createdDate`; otherwise its value is implicitly `createdDate`. `version` is always retained. The summary compacts metadata, not comment text; use `full` for the complete REST metadata.
- `full`: `root` and `replies` retain the complete REST comment objects, including `extensions.inlineProperties`, `extensions.resolution`, `history`, `version` (latest edit), and `body.storage.value`. Read the thread's resolution from the root, not replies.
- `none`: no automatic comment requests, including footer counting. Pages/blogposts still include `inlineComments: {"mode":"none","footerCommentsIncluded":false}`, with no counts or threads, explicitly indicating that discussion was not requested. Reading a comment or attachment by ID skips comment loading and returns the raw content response unchanged.

Confluence's `depth=all` response is flat, not nested under `children.comment.results`. After collecting all REST pages, the tool starts a thread at each nonempty `extensions.inlineProperties.originalSelection` and attaches subsequent quote-less replies to it, including across pagination boundaries. This follows observed Server 7.9.0 ordering, **not a documented parent-ID guarantee**; a changed ordering could misattribute replies. A reply before any root produces a warning rather than a fabricated relationship. No per-root requests are made.

`originalSelection` is the quote at creation time, not a unique/current anchor. Keep `markerRef` when relating a discussion to the body; markers may be missing after edits or occur multiple times. Neither field guarantees an unambiguous current location. `history` records creation; `version` supplies the latest edit metadata, not the complete edit history.

With no inline/resolved comments, `counts` are both zero and `threads` is empty. If inline comment fetching or grouping fails, the page still succeeds, with `inlineComments.warning` and without `counts`/`threads`; this means discussion is unavailable, **not absent**. If only footer counting fails, the page and inline discussion are retained, `warning` describes the footer failure, and `footerComments` is omitted rather than reported as zero.

For insight across pages, the existing search tool can select comments directly: `{"cql":"type=comment AND container=245617220","limit":100,"compact":true,"expand":"content.extensions.inlineProperties,content.extensions.resolution"}`. Results are paginated: advance `start` by the returned `size` until `start + size >= totalSize`, even if `_links.next` is absent. An empty page before that boundary does not prove that all results were retrieved.

### `confluence_search_content`

Search with `GET /search` using caller-supplied CQL, passed through unchanged.

**Arguments:**
- `cql` (string, required): Non-empty Confluence Query Language search string.
- `limit` (number, optional): Page size; defaults to `25` when omitted.
- `start` (number, optional): Zero-based starting index; omitted values use Confluence's default.
- `expand` (string, optional): Comma-separated native search expansions, such as `content.body.storage,content.space`.
- `compact` (boolean, optional): Defaults to `false`. With `true`, omit only `results[].content._expandable`, `results[].iconCssClass`, and `results[].friendlyLastModified`. Non-boolean values are rejected before any request. This local option is not sent to Confluence.

The response retains Confluence's paginated `SearchResult` shape: content is nested under `results[].content`, and space results are nested under `results[].space`. It is not flattened into a content array. Omitted or `false` `compact` returns the REST response unchanged, including formatting. `compact: true` preserves all other fields and values, including links, excerpts, timestamps, explicitly expanded bodies, pagination, and unknown fields; JSON formatting may change. No text is truncated.

For initial navigation, request `limit: 5` and `compact: true`; the default limit remains `25`. The tool fetches only one result page per call. To retrieve all matches, advance `start` by the returned `size` while `start + size < totalSize`. Confluence can omit `_links.next` even when more results exist; do not use its absence as an end-of-results signal. Use search-specific expansion paths to read nested fields, for example:

```json
{
  "cql": "type=page",
  "limit": 5,
  "start": 0,
  "compact": true,
  "expand": "content.body.storage,content.space"
}
```

### `confluence_create_content`

Create a page or blog post with `POST /content`.

**Arguments:**
- `title` (string, required): Non-empty title.
- `spaceKey` (string, required): Non-empty key of the destination space.
- `content` (string, required): Non-empty body in Confluence **storage** format, for example `<p>Hello</p>`; not Markdown.
- `type` (string, optional): `page` or `blogpost`; omitted or empty values default to `page`.
- `parentId` (string, optional): Parent content ID, used to set the ancestors for a child page. Omit for standalone content.

### `confluence_update_content`

Fetch current content with `GET /content/{contentId}?expand=body.storage,version,space`, then update with `PUT /content/{contentId}`. Neither request uses pagination.

**Arguments:**
- `contentId` (string, required): Non-empty content ID; IDs containing `/` or `..` are rejected before a request.
- `version` (number, optional): Target version, from `1` through `2147483647`; defaults to the current version plus one.
- `title` (string, optional): Replacement title; omitted or empty values preserve the fetched title.
- `content` (string, optional): Replacement body in storage format; omitted or empty values preserve the fetched body.
- `versionComment` (string, optional): Comment for the new version.

The fetched type and space are preserved. Fields being reused must be complete; otherwise the update fails before the PUT. For a preserved page or blog post body, `body.storage.value` must be present and non-null. A fetched empty string is valid and is preserved. Legitimately bodyless content types do not require a storage body.

Without an explicit `version`, the current version must be available and incrementing it must remain within the allowed range. An explicit target version is sent as supplied, even if the fetched version is absent; it is not replaced by the default or forced to match the fetched snapshot. API conflicts are returned as tool errors, with no automatic retry or rebase.

### `confluence_list_spaces`

List or search spaces using `GET /search`, preserving the native paginated search response with space data under `results[].space`.

**Arguments:**
- `searchText` (string, optional): Search space titles with CQL `title ~ "..."`. Omitted or empty values use `type=space`.
- `limit` (number, optional): Page size; defaults to `25` when omitted.
- `start` (number, optional): Zero-based starting index; omitted values use Confluence's default.
- `expand` (string, optional): Comma-separated native search expansions, such as `space.homepage`.

This tool returns one result page, including when `searchText` is omitted. Advance `start` to retrieve further pages. Searches match titles, not descriptions. Backslashes and quotes are escaped for containment in the CQL string; the `~` operator's Lucene search syntax retains its meaning.

```json
{
  "searchText": "Documentation",
  "limit": 25,
  "start": 0,
  "expand": "space.homepage"
}
```

### `confluence_get_image`

Retrieve an image attachment by its attachment content ID, not its page ID.

**Arguments:**
- `attachmentId` (string, required): Non-empty attachment ID; IDs containing `/` or `..` are rejected before a request. The ID is escaped as a single URL path segment.
- `downloadDir` (string, optional): An existing absolute directory on the **MCP server's filesystem**, explicitly chosen by the caller. Omitted or empty values return the image without writing to disk. Relative paths are rejected before any HTTP request.

The tool fetches metadata with `GET /content/{attachmentId}`, requires `type: "attachment"`, and follows `_links.download` only within the configured Confluence origin and site context under `/download/attachments/...`. Redirects are not followed. Downloads are limited to **5 MiB of actual bytes**. PNG, JPEG, GIF, and WebP are identified from the bytes, not trusted metadata. Original bytes are preserved without resizing or conversion.

**Without `downloadDir` (or with `"downloadDir": ""`):** the result contains native MCP image content with the original bytes and accompanying text. No file is created.

**With a non-empty `downloadDir`:** the directory must already exist. It may differ from the server's working directory; directory symlinks are followed using native filesystem behavior. The server does not create directories, fall back to `cwd`, or impose a configured workspace root or sandbox. It uses `os.CreateTemp` to create a unique `confluence-attachment-<id>-<random>.<png|jpeg|gif|webp>` file, with mode `0600` on Unix, without overwriting an existing file. If writing or closing fails, the server attempts to remove the partial file and reports any cleanup error.

The saved-file result is **text only**: a summary instructing the caller to **open the file with a local image reader and delete it when finished**, followed by a JSON array with `id`, `path`, `mimeType`, and `size` (bytes), for example:

```json
[{"id":"67890","path":"/absolute/path/to/images/confluence-attachment-67890-123456789.png","mimeType":"image/png","size":1024}]
```

This mode returns no image content, base64, or structured output. Files remain until the caller deletes them.

To discover attachment IDs, use the existing `confluence_search_content` tool with CQL such as:

```json
{"cql":"type = attachment AND container = 12345"}
```

Use each attachment's `results[].content.id` as `attachmentId`; paginate search results as needed. There is no separate attachment-listing tool. For example, return an image directly with `{"attachmentId":"67890"}`, or save it with `{"attachmentId":"67890","downloadDir":"/absolute/path/to/images"}`.

</details>

<details>
<summary>Development, CI, and manual releases</summary>

### Development

The executable is `confluence-mcp` (`confluence-mcp.exe` on Windows). The Go module is [`github.com/0FL01/confluence-7.9.0-go-mcp`](https://github.com/0FL01/confluence-7.9.0-go-mcp); the MCP server name remains `confluence-7.9.0-go-mcp`, with application version `1.0.0`. Dependencies are pinned in `go.mod`, including `github.com/mark3labs/mcp-go` v0.43.2 and `github.com/joho/godotenv` v1.5.1.

Use Go 1.25.5 and run checks from the repository root:

```bash
go vet ./...
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
go build -v -o confluence-mcp .
```

Configuration tests isolate the working directory and `CONFLUENCE_LOGIN`, `CONFLUENCE_PASS`, `CONFLUENCE_BASE_URL`, `CONFLUENCE_API_BASE_PATH`, `CONFLUENCE_HOST`, and the unused legacy `CONFLUENCE_API_TOKEN` (unset differs from empty); no production credentials are used. Do not run working-directory or environment-mutating tests in parallel. The automated tests use local HTTP fixtures to verify client contracts; they do not establish compatibility against a live Confluence installation.

For local development, run `go run .` from the repository root with a `.env` there or with the environment variables set.

### CI and release builds

Local builds use `go build`. GitHub Actions keeps the Test and Lint gates, validates `.goreleaser.yaml` with `goreleaser check`, and builds all six platforms with `goreleaser release --snapshot --clean`. Snapshot builds do not publish releases.

GoReleaser v2 builds the root package for Linux, macOS, and Windows on amd64 and arm64, with `CGO_ENABLED=0` and `-s -w`. It packages the archives listed above and generates `checksums.txt`.

### Publish a release manually

The Release workflow runs **only through `workflow_dispatch`**. After pushing the intended changes to `main`:

1. Open [Actions → Release](https://github.com/0FL01/confluence-7.9.0-go-mcp/actions/workflows/release.yml).
2. Select **Run workflow** and choose branch **main**.
3. Enter the required **version**, for example `v1.0.0`.
4. Select **Run workflow** to publish the release.

The version must be stable `vMAJOR.MINOR.PATCH`, with no leading zeros (except the number `0` itself). An existing version fails. The workflow checks the selected `main` commit before creating and pushing a new tag for that SHA, then runs `goreleaser release --clean`. A tag push alone does not trigger publishing.

If a failure occurs after the tag is pushed, a tag or draft release may remain. Resolve that state manually; the workflow does not force-update tags or automatically clean up a failed release.

</details>

<details>
<summary>Confluence API references</summary>

- [Confluence Server REST API 7.9.0](https://docs.atlassian.com/ConfluenceServer/rest/7.9.0/) — pinned REST specification for the target version.
- [Confluence 7.9 release notes](https://confluence.atlassian.com/doc/confluence-7-9-release-notes-1026537698.html).
- [CQL field reference](https://developer.atlassian.com/server/confluence/cql-field-reference/).
- [Advanced searching using CQL](https://developer.atlassian.com/server/confluence/advanced-searching-using-cql/).

</details>

## License

This project is licensed under the [MIT License](LICENSE). The original license and copyright are preserved.

<details>
<summary>Attribution and upstream release history</summary>

Historical attribution:
- Upstream Go implementation: [Anudeep Dhavaleswarapu's atlassian-confluence-dc-go-mcp](https://github.com/anudeepd/atlassian-confluence-dc-go-mcp).
- Original TypeScript implementation that inspired the Go rewrite: [b1ff/atlassian-dc-mcp](https://github.com/b1ff/atlassian-dc-mcp).
- MCP Go SDK: [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go).
- [Model Context Protocol](https://modelcontextprotocol.io/).

For issues or contributions, use the [canonical GitHub repository](https://github.com/0FL01/confluence-7.9.0-go-mcp).

### Upstream release history

These versions belong to the original upstream module, not to this repository's canonical module or releases. Historical older-Go workarounds are not supported here; use Go 1.25.5.

- **v1.0.2:** documented building with older Go versions.
- **v1.0.1 (deprecated upstream):** fixed the upstream module path; later deprecated for configuration issues.
- **v1.0.0 (deprecated upstream):** initial Go rewrite, core Confluence tools, multi-platform builds and MCP support; later deprecated for module configuration issues.

</details>
