package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestHandleGetSpace(t *testing.T) {
	for _, tc := range []struct {
		name, rpcBody, wantStatus    string
		include, assignmentsComplete bool
	}{
		{"metadata only", "", "not_requested", false, false},
		{"assignments", `{"result":[{"type":"VIEWSPACE","spacePermissions":[{"type":"VIEWSPACE","userName":"alice","groupName":null},{"type":"VIEWSPACE","userName":null,"groupName":"editors"},{"type":"VIEWSPACE","userName":null,"groupName":null}]}]}`, "ok", true, true},
		{"empty", `{"result":[]}`, "empty", true, true},
		{"forbidden", `{"error":{"code":0,"message":"NotPermittedException: SECRET_MARKER"}}`, "forbidden", true, false},
		{"unsupported", `{"error":{"code":-32601,"message":"SECRET_MARKER"}}`, "unsupported", true, false},
		{"missing result", `{}`, "unknown", true, false},
		{"missing assignments", `{"result":[{"type":"VIEWSPACE"}]}`, "unknown", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				login, pass, ok := r.BasicAuth()
				if !ok || login != "test-user" || pass != "test-password" || r.Header.Get("Cookie") != "" {
					t.Error("wrong auth")
				}
				switch r.URL.Path {
				case "/wiki/rest/api/space/TEST":
					if r.Method != http.MethodGet || r.URL.Query().Get("expand") != "homepage" {
						t.Error("wrong metadata request")
					}
					_, _ = w.Write([]byte(`{"id":10,"key":"TEST","name":"Test","type":"global","homepage":{"id":"20","title":"Home"}}`))
				case "/wiki/rpc/json-rpc/confluenceservice-v2":
					var body struct {
						Method string   `json:"method"`
						Params []string `json:"params"`
					}
					if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Params) != 1 || body.Params[0] != "TEST" {
						t.Error("wrong read-only RPC request")
					}
					switch body.Method {
					case "getSpacePermissionSets":
						_, _ = w.Write([]byte(tc.rpcBody))
					case "getPermissions":
						_, _ = w.Write([]byte(`{"result":["VIEWSPACE"]}`))
					default:
						t.Errorf("unexpected RPC method %q", body.Method)
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL + "/wiki/rest/api", Login: "test-user", Password: "test-password"})
			result, err := handleGetSpace(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"spaceKey": "TEST", "includePermissions": tc.include}}})
			if err != nil || result.IsError {
				t.Fatalf("result: %v %v", result, err)
			}
			text := result.Content[0].(mcp.TextContent).Text
			if strings.Contains(text, "SECRET_MARKER") {
				t.Fatal("RPC error text leaked")
			}
			var got struct {
				Permissions spaceAccess     `json:"permissions"`
				Operations  spaceOperations `json:"currentUserOperations"`
				Complete    bool            `json:"complete"`
			}
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatal(err)
			}
			if got.Permissions.Status != tc.wantStatus || got.Permissions.AssignmentsComplete != tc.assignmentsComplete || got.Permissions.AnonymousAccess != "unknown" {
				t.Fatalf("permissions: %+v", got.Permissions)
			}
			if tc.include {
				if calls != 3 || got.Complete || !got.Operations.Complete || got.Operations.Status != "ok" {
					t.Fatalf("completeness/calls: %+v, %d", got, calls)
				}
			} else if calls != 1 || !got.Complete {
				t.Fatalf("unrequested permissions caused extra calls or incomplete metadata: %d %+v", calls, got)
			}
			if tc.name == "assignments" {
				if len(got.Permissions.Assignments) != 3 || got.Permissions.Assignments[0].SubjectType != "user" || got.Permissions.Assignments[1].SubjectType != "group" || got.Permissions.Assignments[2].SubjectType != "unknown" {
					t.Fatalf("subjects: %+v", got.Permissions.Assignments)
				}
			}
		})
	}
}

func TestSafeAPIErrorAndRedirect(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTemporaryRedirect} {
		for _, decode := range []bool{false, true} {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("SECRET_AUTHORIZATION_COOKIE_CONFIG"))
			}))
			client := NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL, Login: "test-user", Password: "test-password"})
			var err error
			if decode {
				var value any
				err = client.getJSON(context.Background(), "/content/1", nil, &value)
			} else {
				_, err = client.doRequest(context.Background(), http.MethodPost, "/content", nil, map[string]string{"type": "page"})
			}
			server.Close()
			if err == nil || strings.Contains(err.Error(), "SECRET") || calls != 1 {
				t.Fatalf("status=%d decode=%v err=%v calls=%d", status, decode, err, calls)
			}
		}
	}
}
