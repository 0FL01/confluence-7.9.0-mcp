package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func restrictionTestClient(t *testing.T, handler http.HandlerFunc) *ConfluenceClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		login, pass, ok := r.BasicAuth()
		if !ok || login != "fixture-user" || pass != "fixture-pass" {
			t.Error("missing fixture Basic authentication")
		}
		if !strings.HasPrefix(r.URL.Path, "/wiki/rest/") {
			t.Errorf("lost site context: %s", r.URL.Path)
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return NewConfluenceClient(&ConfluenceConfig{BaseURL: server.URL + "/wiki/rest/api", Login: "fixture-user", Password: "fixture-pass"})
}

func restrictionTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func restrictionTestPage(subjects []string, key string, start, limit int) map[string]any {
	results := []map[string]any{}
	for i := start; i < len(subjects) && i < start+limit; i++ {
		results = append(results, map[string]any{key: subjects[i]})
	}
	return map[string]any{"results": results, "start": start, "limit": limit, "size": len(results)}
}

func restrictionTestACL(operation string, users, groups []string, start, userLimit, groupLimit int) map[string]any {
	return map[string]any{"operation": operation, "restrictions": map[string]any{
		"user":  restrictionTestPage(users, "userKey", start, userLimit),
		"group": restrictionTestPage(groups, "name", start, groupLimit),
	}}
}

func restrictionTestDecode(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("expected one JSON result: %#v", result)
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("not text: %#v", result.Content)
	}
	if strings.Contains(text.Text, "PRIVATE-ERROR-BODY") {
		t.Fatal("raw error body disclosed")
	}
	if err := json.Unmarshal([]byte(text.Text), target); err != nil {
		t.Fatalf("decode result: %v; %s", err, text.Text)
	}
}

func TestGetRestrictionsPaginationAndAncestors(t *testing.T) {
	var mu sync.Mutex
	starts := map[string][]int{}
	client := restrictionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/wiki/rest/api/content/10" {
			if r.URL.Query().Get("expand") != "ancestors" {
				t.Error("ancestors not expanded")
			}
			restrictionTestJSON(w, map[string]any{"id": "10", "title": "Child", "type": "page", "ancestors": []any{map[string]any{"id": "1", "title": "Parent"}}})
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) != 9 || parts[6] != "restriction" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		id, operation := parts[5], parts[8]
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		starts[id+":"+operation] = append(starts[id+":"+operation], start)
		if id == "1" {
			if operation != "read" {
				t.Error("ancestor update restrictions must not be requested")
			}
			restrictionTestJSON(w, restrictionTestACL(operation, []string{"parent-user"}, nil, start, 2, 2))
			return
		}
		if operation == "read" {
			// No _links.next. Independent server-enforced page limits.
			restrictionTestJSON(w, restrictionTestACL(operation, []string{"a", "b", "c"}, []string{"g1", "g2"}, start, 2, 1))
		} else {
			restrictionTestJSON(w, restrictionTestACL(operation, []string{"editor"}, nil, start, 2, 2))
		}
	})
	result, err := handleGetRestrictions(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"contentId": "10", "includeAncestors": true}}})
	if err != nil || result.IsError {
		t.Fatalf("get failed: %v %#v", err, result)
	}
	var snapshot restrictionSnapshot
	restrictionTestDecode(t, result, &snapshot)
	if !snapshot.Complete || len(snapshot.Own.Read.Users) != 3 || len(snapshot.Own.Read.Groups) != 2 || len(snapshot.Own.Update.Users) != 1 {
		t.Fatalf("incomplete pagination: %+v", snapshot)
	}
	if len(snapshot.Ancestors.Sources) != 1 || snapshot.Ancestors.Sources[0].Source.ID != "1" || snapshot.Ancestors.Sources[0].Read.Users[0]["userKey"] != "parent-user" {
		t.Fatalf("ancestor source not separate: %+v", snapshot.Ancestors)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(starts["10:read"], []int{0, 1, 2}) || len(starts["1:update"]) != 0 {
		t.Fatalf("wrong pagination or inheritance: %+v", starts)
	}
}

func TestGetRestrictionsIncompleteData(t *testing.T) {
	for _, scenario := range []string{"forbidden-own", "forbidden-ancestor", "missing-arrays", "missing-metadata", "repeated-subject", "wrong-start", "missing-ancestors", "target-forbidden"} {
		t.Run(scenario, func(t *testing.T) {
			client := restrictionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/wiki/rest/api/content/10" {
					if scenario == "target-forbidden" {
						http.Error(w, "PRIVATE-ERROR-BODY", http.StatusForbidden)
						return
					}
					page := map[string]any{"id": "10", "title": "Child", "type": "page"}
					if scenario != "missing-ancestors" {
						page["ancestors"] = []any{map[string]any{"id": "1", "title": "Parent"}}
					}
					restrictionTestJSON(w, page)
					return
				}
				operation := "read"
				if strings.HasSuffix(r.URL.Path, "/update") {
					operation = "update"
				}
				ancestor := strings.Contains(r.URL.Path, "/content/1/")
				if (scenario == "forbidden-own" && operation == "update") || (scenario == "forbidden-ancestor" && ancestor) {
					http.Error(w, "PRIVATE-ERROR-BODY", http.StatusForbidden)
					return
				}
				start, _ := strconv.Atoi(r.URL.Query().Get("start"))
				value := restrictionTestACL(operation, nil, nil, start, 2, 2)
				if operation == "read" && !ancestor {
					switch scenario {
					case "missing-arrays":
						value["restrictions"].(map[string]any)["user"].(map[string]any)["results"] = nil
					case "missing-metadata":
						delete(value["restrictions"].(map[string]any)["user"].(map[string]any), "limit")
					case "repeated-subject":
						value["restrictions"].(map[string]any)["user"] = map[string]any{"results": []any{map[string]any{"userKey": "same"}}, "start": start, "limit": 1, "size": 1}
					case "wrong-start":
						value["restrictions"].(map[string]any)["user"].(map[string]any)["start"] = 20
					}
				}
				restrictionTestJSON(w, value)
			})
			result, err := handleGetRestrictions(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"contentId": "10", "includeAncestors": true}}})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "target-forbidden" {
				if !result.IsError || strings.Contains(result.Content[0].(mcp.TextContent).Text, "PRIVATE-ERROR-BODY") {
					t.Fatalf("target error not sanitized: %#v", result)
				}
				return
			}
			var snapshot restrictionSnapshot
			restrictionTestDecode(t, result, &snapshot)
			if snapshot.Complete || len(snapshot.Warnings) == 0 {
				t.Fatalf("incomplete data reported complete: %+v", snapshot)
			}
			if scenario == "forbidden-own" && (!snapshot.Own.Read.Complete || snapshot.Own.Update.Complete) {
				t.Fatal("own operation failures were not kept separate")
			}
		})
	}
}

type restrictionWriteFixture struct {
	mu            sync.Mutex
	users         []string
	groups        []string
	editors       []string
	editorGroups  []string
	writes        []string
	failSubject   string
	failStatus    int
	ambiguous     bool
	loseRead      bool
	changeOnGuard bool
	readCalls     int
}

func (f *restrictionWriteFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodGet {
		if f.loseRead && len(f.writes) > 0 {
			http.Error(w, "PRIVATE-ERROR-BODY", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/wiki/rest/api/content/10" {
			restrictionTestJSON(w, map[string]any{"id": "10", "title": "Target", "type": "page"})
			return
		}
		operation := "read"
		users, groups := f.users, f.groups
		if strings.HasSuffix(r.URL.Path, "/update") {
			operation, users, groups = "update", f.editors, f.editorGroups
		} else {
			f.readCalls++
			if f.changeOnGuard && f.readCalls == 2 {
				f.users, f.groups = []string{"old"}, nil
				users, groups = f.users, f.groups
			}
		}
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		restrictionTestJSON(w, restrictionTestACL(operation, users, groups, start, 200, 200))
		return
	}
	readOperation := strings.HasPrefix(r.URL.Path, "/wiki/rest/experimental/content/10/restriction/byOperation/read/")
	updateOperation := strings.HasPrefix(r.URL.Path, "/wiki/rest/experimental/content/10/restriction/byOperation/update/")
	if !readOperation && !updateOperation {
		t.Errorf("non-granular or wrong-operation mutation: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(400)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		t.Errorf("unexpected mutation method %s", r.Method)
	}
	subject := r.URL.Query().Get("key")
	list := &f.users
	if updateOperation {
		list = &f.editors
	}
	if strings.Contains(r.URL.Path, "/group/") {
		subject = strings.SplitN(r.URL.Path, "/group/", 2)[1]
		list = &f.groups
		if updateOperation {
			list = &f.editorGroups
		}
	}
	f.writes = append(f.writes, r.Method+" "+r.URL.RequestURI())
	if subject == f.failSubject && !f.ambiguous {
		http.Error(w, "PRIVATE-ERROR-BODY", f.failStatus)
		return
	}
	if r.Method == http.MethodPut {
		if !slices.Contains(*list, subject) {
			*list = append(*list, subject)
		}
	} else {
		*list = slices.DeleteFunc(*list, func(value string) bool { return value == subject })
	}
	if subject == f.failSubject && f.ambiguous {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close() // Server applied the write but no response reached the client.
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func restrictionTestUpdate(t *testing.T, f *restrictionWriteFixture, args map[string]any) (*mcp.CallToolResult, restrictionUpdateResult) {
	t.Helper()
	client := restrictionTestClient(t, func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) })
	args["contentId"] = "10"
	if _, exists := args["operation"]; !exists {
		args["operation"] = "read"
	}
	result, err := handleUpdateRestrictions(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded restrictionUpdateResult
	restrictionTestDecode(t, result, &decoded)
	return result, decoded
}

func TestUpdateRestrictionsDryRun(t *testing.T) {
	f := &restrictionWriteFixture{users: []string{"old", "keep"}, groups: []string{"existing"}, editors: []string{"editor"}}
	result, decoded := restrictionTestUpdate(t, f, map[string]any{"addUsers": []any{"new", "keep"}, "removeGroups": []any{"absent"}, "removeUsers": []any{"old"}})
	if result.IsError || !decoded.DryRun || len(decoded.Actions) != 2 || len(decoded.Noops) != 2 || decoded.Readback != nil {
		t.Fatalf("wrong dry run: %+v", decoded)
	}
	if decoded.Preflight.Ancestors.Requested || decoded.Preflight.Ancestors.Status != "not-requested" {
		t.Fatal("unrequested ancestors not explicit")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 0 || !reflect.DeepEqual(f.users, []string{"old", "keep"}) {
		t.Fatal("dry run mutated restrictions")
	}
}

func TestUpdateRestrictionsGranularPreservationAndReadback(t *testing.T) {
	f := &restrictionWriteFixture{users: []string{"old", "keep"}, groups: []string{"existing"}, editors: []string{"editor"}}
	result, decoded := restrictionTestUpdate(t, f, map[string]any{"dryRun": false, "addUsers": []any{"new&key", "keep"}, "addGroups": []any{"team/A +%?"}, "removeUsers": []any{"old"}})
	if result.IsError || decoded.ReadbackStatus != "complete" || decoded.Readback == nil {
		t.Fatalf("write not verified: %+v", decoded)
	}
	for _, action := range decoded.Actions {
		if action.Status != "confirmed" || action.ReadbackMatches == nil || !*action.ReadbackMatches {
			t.Fatalf("action not verified: %+v", action)
		}
	}
	if len(decoded.Readback.Own.Read.Users) != 2 || len(decoded.Readback.Own.Read.Groups) != 2 || decoded.Readback.Own.Update.Users[0]["userKey"] != "editor" {
		t.Fatalf("unrelated ACL lost: %+v", decoded.Readback)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 3 || !strings.HasPrefix(f.writes[0], "PUT ") || !strings.HasPrefix(f.writes[1], "PUT ") || !strings.HasPrefix(f.writes[2], "DELETE ") || !strings.Contains(f.writes[0], "key=new%26key") || !strings.Contains(f.writes[1], "/group/team%2FA%20+%25%3F") {
		t.Fatalf("wrong write ordering or escaping: %+v", f.writes)
	}
}

func TestUpdateRestrictionsLastRemoval(t *testing.T) {
	for _, tc := range []struct{ dry, confirm, wantError bool }{{true, false, false}, {false, false, true}, {false, true, false}} {
		t.Run(fmt.Sprintf("dry=%v/confirm=%v", tc.dry, tc.confirm), func(t *testing.T) {
			f := &restrictionWriteFixture{users: []string{"old"}, groups: []string{"existing"}}
			result, decoded := restrictionTestUpdate(t, f, map[string]any{"dryRun": tc.dry, "confirmRemoveLastRestriction": tc.confirm, "removeUsers": []any{"old"}, "removeGroups": []any{"existing"}})
			if result.IsError != tc.wantError || decoded.ConfirmationRequired != !tc.confirm {
				t.Fatalf("last removal confirmation: %+v", decoded)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			wantWrites := 0
			if !tc.dry && tc.confirm {
				wantWrites = 2
			}
			if len(f.writes) != wantWrites {
				t.Fatalf("writes = %v", f.writes)
			}
		})
	}
}

func TestUpdateRestrictionsUpdateOperationPreservesRead(t *testing.T) {
	f := &restrictionWriteFixture{users: []string{"reader"}, groups: []string{"readers"}, editors: []string{"old-editor", "keep-editor"}, editorGroups: []string{"editors"}}
	result, decoded := restrictionTestUpdate(t, f, map[string]any{
		"operation": "update", "dryRun": false, "addUsers": []any{"new-editor"}, "removeUsers": []any{"old-editor"},
	})
	if result.IsError || decoded.Operation != "update" || decoded.ReadbackStatus != "complete" {
		t.Fatalf("update operation failed: %+v", decoded)
	}
	if !restrictionHas(decoded.Readback.Own.Read, "user", "reader") || !restrictionHas(decoded.Readback.Own.Read, "group", "readers") ||
		!restrictionHas(decoded.Readback.Own.Update, "user", "keep-editor") || !restrictionHas(decoded.Readback.Own.Update, "group", "editors") {
		t.Fatalf("unrelated subjects changed: %+v", decoded.Readback)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 2 {
		t.Fatalf("wrong mutation count: %+v", f.writes)
	}
	for _, write := range f.writes {
		if !strings.Contains(write, "/byOperation/update/user?") {
			t.Fatalf("wrong operation mutated: %s", write)
		}
	}
}

func TestUpdateRestrictionsIncompletePreflightAndReadback(t *testing.T) {
	for _, phase := range []string{"preflight", "readback"} {
		t.Run(phase, func(t *testing.T) {
			var mu sync.Mutex
			writes := 0
			client := restrictionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(204)
					return
				}
				if r.URL.Path == "/wiki/rest/api/content/10" {
					restrictionTestJSON(w, map[string]any{"id": "10", "title": "Target", "type": "page"})
					return
				}
				operation := "read"
				if strings.HasSuffix(r.URL.Path, "/update") {
					operation = "update"
					if phase == "preflight" || writes > 0 {
						http.Error(w, "PRIVATE-ERROR-BODY", http.StatusForbidden)
						return
					}
				}
				users := []string{"keep"}
				if writes > 0 {
					users = append(users, "new")
				}
				restrictionTestJSON(w, restrictionTestACL(operation, users, nil, 0, 200, 200))
			})
			result, err := handleUpdateRestrictions(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{
				"contentId": "10", "operation": "read", "dryRun": false, "addUsers": []any{"new"},
			}}})
			if err != nil || !result.IsError {
				t.Fatalf("incomplete %s passed: %v %#v", phase, err, result)
			}
			var decoded restrictionUpdateResult
			restrictionTestDecode(t, result, &decoded)
			mu.Lock()
			defer mu.Unlock()
			if phase == "preflight" {
				if writes != 0 || decoded.Preflight.Complete {
					t.Fatalf("incomplete preflight allowed mutation: %+v", decoded)
				}
			} else if writes != 1 || decoded.ReadbackStatus != "incomplete" || decoded.Actions[0].Status != "unknown" {
				t.Fatalf("incomplete readback hidden: %+v", decoded)
			}
		})
	}
}

func TestUpdateRestrictionsConcurrentLastRemovalGuard(t *testing.T) {
	f := &restrictionWriteFixture{users: []string{"old", "other"}, changeOnGuard: true}
	result, decoded := restrictionTestUpdate(t, f, map[string]any{"dryRun": false, "removeUsers": []any{"old"}})
	if !result.IsError || !decoded.ConfirmationRequired || decoded.Actions[0].Status != "failed" {
		t.Fatalf("concurrent last removal not stopped: %+v", decoded)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 0 {
		t.Fatal("removed last restriction after concurrent change")
	}
}

func TestUpdateRestrictionsPartialAndAmbiguous(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		httpStatus   int
		ambiguous    bool
	}{{"forbidden", "failed", 403, false}, {"server-error", "unknown", 500, false}, {"applied-disconnect", "unknown", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &restrictionWriteFixture{users: []string{"keep"}, failSubject: "second", failStatus: tc.httpStatus, ambiguous: tc.ambiguous}
			result, decoded := restrictionTestUpdate(t, f, map[string]any{"dryRun": false, "addUsers": []any{"first", "second", "third"}})
			if !result.IsError || decoded.ReadbackStatus != "complete" || decoded.Actions[0].Status != "confirmed" || decoded.Actions[1].Status != tc.status || decoded.Actions[2].Status != "not-attempted" {
				t.Fatalf("partial outcome hidden: %+v", decoded)
			}
			if !restrictionHas(decoded.Readback.Own.Read, "user", "keep") || !restrictionHas(decoded.Readback.Own.Read, "user", "first") || restrictionHas(decoded.Readback.Own.Read, "user", "second") != tc.ambiguous {
				t.Fatalf("actual readback missing: %+v", decoded.Readback)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.writes) != 2 {
				t.Fatalf("write replay/continued after failure: %+v", f.writes)
			}
		})
	}
}

func TestUpdateRestrictionsLostReadAccess(t *testing.T) {
	f := &restrictionWriteFixture{users: []string{"old"}, loseRead: true}
	result, decoded := restrictionTestUpdate(t, f, map[string]any{"dryRun": false, "addUsers": []any{"new"}})
	if !result.IsError || decoded.ReadbackStatus != "failed" || decoded.Actions[0].Status != "unknown" {
		t.Fatalf("lost read access treated as success: %+v", decoded)
	}
}

func TestRestrictionsRejectMalformedArgumentsBeforeRequests(t *testing.T) {
	client := restrictionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("invalid arguments made request: %s", r.URL.Path)
		w.WriteHeader(500)
	})
	for _, bad := range []map[string]any{
		{"contentId": "../10"}, {"contentId": 10}, {"operation": "delete"}, {"dryRun": "false"}, {"dryRun": nil},
		{"confirmRemoveLastRestriction": 1}, {"addUsers": "key"}, {"addUsers": []any{1}}, {"addGroups": []any{" "}},
		{"addGroups": []any{".."}}, {"addUsers": []any{"same"}, "removeUsers": []any{"same"}}, {"addGroups": []any{"same"}, "removeGroups": []any{"same"}},
	} {
		args := map[string]any{"contentId": "10", "operation": "read", "dryRun": false}
		for key, value := range bad {
			args[key] = value
		}
		result, err := handleUpdateRestrictions(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
		if err != nil || !result.IsError {
			t.Fatalf("invalid arguments accepted: %+v", bad)
		}
	}
	result, err := handleGetRestrictions(client)(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"contentId": "10", "includeAncestors": "true"}}})
	if err != nil || !result.IsError {
		t.Fatal("nonboolean includeAncestors accepted")
	}
}
