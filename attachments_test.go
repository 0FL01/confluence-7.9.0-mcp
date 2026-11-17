package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestHandleUploadAttachmentCreate(t *testing.T) {
	for _, tc := range []struct {
		name, mediaType string
		data            []byte
	}{
		{"image.png", "image/png", []byte("\x89PNG\r\n\x1a\noriginal\x00bytes")},
		{"notes.md", "text/markdown", []byte("# Original markdown\n")},
		{"report.json", "application/json", []byte(`{"answer":42}`)},
		{"данные.json", "application/json", []byte(`{"answer":42}`)},
		// Larger than the independent image download cap; uploads have no such cap.
		{"events.jsonl", "application/x-ndjson", bytes.Repeat([]byte("{\"value\":1}\n"), 500000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filePath := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(filePath, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/wiki/rest/api/content/page/child/attachment" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if login, password, ok := r.BasicAuth(); !ok || login != "test-user" || password != "test-password" {
					t.Error("missing authentication")
				}
				if r.Method == http.MethodGet {
					if r.URL.Query().Get("filename") != tc.name {
						t.Errorf("filename precheck: %s", r.URL.RawQuery)
					}
					_, _ = io.WriteString(w, `{"results":[]}`)
					return
				}
				posts.Add(1)
				if r.Method != http.MethodPost || r.Header.Get("X-Atlassian-Token") != "nocheck" || r.URL.Query().Get("allowDuplicated") != "false" {
					t.Error("missing create-only POST contract")
				}
				if r.ContentLength != -1 {
					t.Errorf("expected streaming request, length=%d", r.ContentLength)
				}
				multipartReader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				part, err := multipartReader.NextPart()
				if err != nil {
					t.Error(err)
					return
				}
				if part.FormName() != "file" || part.FileName() != tc.name || part.Header.Get("Content-Type") != tc.mediaType {
					t.Errorf("wrong file part: %v", part.Header)
				}
				data, err := io.ReadAll(part)
				if err != nil || !bytes.Equal(data, tc.data) {
					t.Errorf("original file bytes changed: %v", err)
				}
				part, err = multipartReader.NextPart()
				if err != nil {
					t.Error(err)
					return
				}
				comment, _ := io.ReadAll(part)
				if part.FormName() != "comment" || string(comment) != "publication comment" {
					t.Error("missing comment")
				}
				if _, err := multipartReader.NextPart(); err != io.EOF {
					t.Errorf("unexpected multipart trailer: %v", err)
				}
				_, _ = fmt.Fprintf(w, `{"results":[{"id":"att","type":"attachment","title":%q,"container":{"id":"page"},"version":{"number":1},"metadata":{"mediaType":%q},"extensions":{"fileSize":%d},"_links":{"download":"/download/attachments/page/file?version=1"}}]}`, tc.name, tc.mediaType, len(tc.data))
			}))
			defer server.Close()
			client := NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL + "/wiki/rest/api", Login: "test-user", Password: "test-password"})
			result, err := handleUploadAttachment(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"contentId": "page", "filePath": filePath, "comment": "publication comment"}}})
			if err != nil || result.IsError {
				t.Fatalf("upload failed: %v, %+v", err, result)
			}
			var uploaded uploadedAttachment
			if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &uploaded); err != nil {
				t.Fatal(err)
			}
			if posts.Load() != 1 || uploaded.AttachmentID != "att" || uploaded.ContentID != "page" || uploaded.FileName != tc.name || uploaded.MediaType != tc.mediaType || uploaded.FileSize != int64(len(tc.data)) || uploaded.Version != 1 || uploaded.DownloadURL != server.URL+"/wiki/download/attachments/page/file?version=1" {
				t.Fatalf("unexpected upload result: %+v, posts=%d", uploaded, posts.Load())
			}
		})
	}
}

func TestHandleUploadAttachmentConflictAndMembership(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(filePath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, attachmentID, container, contentType, conflictID string
	}{
		{name: "create conflict", conflictID: "other"},
		{name: "wrong page", attachmentID: "att", container: "other", contentType: "attachment"},
		{name: "wrong type", attachmentID: "att", container: "page", contentType: "page"},
		{name: "update filename conflict", attachmentID: "att", container: "page", contentType: "attachment", conflictID: "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					posts.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if r.URL.Path == "/rest/api/content/att" {
					_, _ = fmt.Fprintf(w, `{"id":"att","type":%q,"container":{"id":%q},"version":{"number":2}}`, tc.contentType, tc.container)
				} else if r.URL.Query().Get("start") == "0" {
					_, _ = fmt.Fprintf(w, `{"results":[{"id":%q,"title":"data.json"}]}`, tc.conflictID)
				} else {
					_, _ = io.WriteString(w, `{"results":[]}`)
				}
			}))
			defer server.Close()
			args := map[string]any{"contentId": "page", "filePath": filePath}
			if tc.attachmentID != "" {
				args["attachmentId"] = tc.attachmentID
			}
			client := NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL + "/rest/api"})
			result, err := handleUploadAttachment(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
			assertToolError(t, result, err)
			if posts.Load() != 0 {
				t.Fatal("preflight failure performed POST")
			}
		})
	}
}

func TestHandleUploadAttachmentUpdateAndReadback(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "local.json")
	if err := os.WriteFile(filePath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	var posts, metadataReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			if r.URL.Path != "/rest/api/content/page/child/attachment/att/data" {
				t.Errorf("incorrect update endpoint: %s", r.URL.Path)
			}
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, `{"id":"att"}`)
			return
		}
		if r.URL.Path == "/rest/api/content/att" {
			metadataReads.Add(1)
			if r.URL.Query().Get("expand") != "container,version" {
				t.Error("missing membership/version expansion")
			}
			_, _ = fmt.Fprintf(w, `{"id":"att","type":"attachment","title":"server.json","container":{"id":"page"},"version":{"number":%d},"extensions":{"fileSize":2,"mediaType":"application/json"},"_links":{"download":"/download/attachments/page/server.json"}}`, 2+posts.Load())
			return
		}
		if r.URL.Query().Get("filename") != "remote.json" {
			t.Error("fileName override was not checked")
		}
		if r.URL.Query().Get("start") == "0" {
			_, _ = io.WriteString(w, `{"results":[{"id":"att","title":"remote.json"}]}`)
		} else {
			_, _ = io.WriteString(w, `{"results":[]}`)
		}
	}))
	defer server.Close()
	client := NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL + "/rest/api"})
	result, err := handleUploadAttachment(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"contentId": "page", "attachmentId": "att", "filePath": filePath, "fileName": "remote.json"}}})
	if err != nil || result.IsError {
		t.Fatalf("update failed: %v, %+v", err, result)
	}
	var uploaded uploadedAttachment
	if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &uploaded); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 || metadataReads.Load() != 2 || uploaded.Version != 3 || uploaded.FileName != "server.json" {
		t.Fatalf("expected one update and server metadata readback: %+v, posts=%d reads=%d", uploaded, posts.Load(), metadataReads.Load())
	}
}

func TestHandleUploadAttachmentAmbiguousNoReplay(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(filePath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"disconnect", "truncated", "invalid JSON", "status"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"results":[]}`)
					return
				}
				posts.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				switch mode {
				case "disconnect":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
				case "truncated":
					w.Header().Set("Content-Length", "1000")
					_, _ = io.WriteString(w, `{"id":"att"}`)
				case "invalid JSON":
					_, _ = io.WriteString(w, "PRIVATE_SERVER_BODY")
				case "status":
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, "PRIVATE_SERVER_BODY")
				}
			}))
			defer server.Close()
			client := NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL + "/rest/api"})
			result, err := handleUploadAttachment(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"contentId": "page", "filePath": filePath}}})
			assertToolError(t, result, err)
			text := result.Content[0].(mcp.TextContent).Text
			if posts.Load() != 1 || strings.Contains(text, "PRIVATE_SERVER_BODY") || (mode != "status" && !strings.Contains(text, "outcome unknown")) {
				t.Fatalf("unsafe ambiguity/replay response: %s, posts=%d", text, posts.Load())
			}
		})
	}
}

func TestHandleUploadAttachmentCreateReadback(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(filePath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("metadataUnavailable=%v", unavailable), func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					_, _ = io.WriteString(w, `{}`)
					return
				}
				if r.URL.Path == "/rest/api/content/att" {
					if unavailable {
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, "PRIVATE_METADATA_ERROR")
						return
					}
					_, _ = io.WriteString(w, `{"id":"att","type":"attachment","title":"data.json","container":{"id":"page"},"version":{"number":1},"extensions":{"mediaType":"application/json","fileSize":2},"_links":{"download":"/download/attachments/page/data.json"}}`)
					return
				}
				if posts.Load() == 1 && r.URL.Query().Get("start") == "0" {
					_, _ = io.WriteString(w, `{"results":[{"id":"att","title":"data.json"}]}`)
				} else {
					_, _ = io.WriteString(w, `{"results":[]}`)
				}
			}))
			defer server.Close()
			client := NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL + "/rest/api"})
			result, err := handleUploadAttachment(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"contentId": "page", "filePath": filePath}}})
			if err != nil || result.IsError != unavailable {
				t.Fatalf("unexpected readback result: %v, %+v", err, result)
			}
			text := result.Content[0].(mcp.TextContent).Text
			if posts.Load() != 1 || strings.Contains(text, "PRIVATE_METADATA_ERROR") || (unavailable && !strings.Contains(text, "upload accepted by server")) {
				t.Fatalf("unsafe readback/replay result: %s; posts=%d", text, posts.Load())
			}
		})
	}
}

func TestHandleUploadAttachmentCancellation(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "large.jsonl")
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"results":[]}`)
			return
		}
		posts.Add(1)
		var prefix [1024]byte
		_, _ = io.ReadFull(r.Body, prefix[:])
		cancel()
	}))
	defer server.Close()
	client := NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL + "/rest/api"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, err := handleUploadAttachment(client)(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"contentId": "page", "filePath": filePath}}})
		assertToolError(t, result, err)
		if !strings.Contains(result.Content[0].(mcp.TextContent).Text, "outcome unknown") {
			t.Error("cancelled write did not report ambiguity")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled streaming upload did not stop")
	}
	if posts.Load() != 1 {
		t.Fatalf("cancelled upload replayed: %d", posts.Load())
	}
}

func TestHandleUploadAttachmentValidation(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(filePath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL + "/rest/api"})
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"contentId", nil}, {"contentId", "a/b"}, {"contentId", ".."},
		{"attachmentId", 123}, {"attachmentId", "a/b"}, {"attachmentId", ""},
		{"filePath", "relative"}, {"filePath", filepath.Dir(filePath)}, {"filePath", filePath + ".missing"}, {"filePath", true},
		{"fileName", false}, {"fileName", "../secret"}, {"fileName", "bad\r\nheader"}, {"fileName", ""},
		{"comment", nil}, {"comment", false},
	} {
		t.Run(fmt.Sprintf("%s=%v", tc.name, tc.value), func(t *testing.T) {
			args := map[string]any{"contentId": "page", "filePath": filePath}
			args[tc.name] = tc.value
			result, err := handleUploadAttachment(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
			assertToolError(t, result, err)
			if tc.value == filePath+".missing" && !strings.Contains(result.Content[0].(mcp.TextContent).Text, "file not found") {
				t.Fatal("missing file was not identified")
			}
		})
	}
	for _, args := range []any{nil, "invalid", map[string]any{"contentId": "page"}, map[string]any{"filePath": filePath}} {
		result, err := handleUploadAttachment(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
		assertToolError(t, result, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid input issued %d requests", requests.Load())
	}
}
