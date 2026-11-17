package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

type spaceAssignment struct {
	Type        string `json:"type"`
	UserName    string `json:"userName,omitempty"`
	GroupName   string `json:"groupName,omitempty"`
	SubjectType string `json:"subjectType"`
}

type spaceAccess struct {
	Status              string            `json:"status"`
	Complete            bool              `json:"complete"`
	AssignmentsComplete bool              `json:"assignmentsComplete"`
	Assignments         []spaceAssignment `json:"assignments"`
	AnonymousAccess     string            `json:"anonymousAccess"`
	Source              string            `json:"source"`
}

type spaceOperations struct {
	Status   string   `json:"status"`
	Complete bool     `json:"complete"`
	Values   []string `json:"values"`
	Source   string   `json:"source"`
}

type spaceMetadata struct {
	ID       int64           `json:"id"`
	Key      string          `json:"key"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Homepage json.RawMessage `json:"homepage"`
}

// spaceRPC calls only the two read-only legacy methods used by get-space. HTTP
// 200 alone is not success: the JSON-RPC envelope can contain an error instead.
func (c *ConfluenceClient) spaceRPC(ctx context.Context, method, key string, target any) (string, error) {
	if method != "getSpacePermissionSets" && method != "getPermissions" {
		return "unsupported", fmt.Errorf("unsupported space read method")
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": []string{key}, "id": 1})
	if err != nil {
		return "error", fmt.Errorf("cannot encode space read request")
	}
	resp, err := c.executeSiteRequest(ctx, http.MethodPost, "/rpc/json-rpc/confluenceservice-v2", nil, bytes.NewReader(body), nil)
	if err != nil {
		return "error", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := "error"
		switch resp.StatusCode {
		case http.StatusForbidden:
			status = "forbidden"
		case http.StatusUnauthorized:
			status = "unauthenticated"
		case http.StatusMethodNotAllowed, http.StatusNotImplemented:
			status = "unsupported"
		case http.StatusNotFound:
			status = "unavailable" // A hidden route and a missing route are indistinguishable.
		}
		return status, responseStatusError(resp)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return "error", fmt.Errorf("invalid space RPC response")
	}
	if envelope.Error != nil {
		if envelope.Error.Code == -32601 {
			return "unsupported", fmt.Errorf("space RPC method is not supported")
		}
		// Classify known permission errors, but never return arbitrary RPC text.
		message := strings.ToLower(envelope.Error.Message + " " + string(envelope.Error.Data))
		for _, marker := range []string{"notpermittedexception", "notauthorizedexception", "permission denied", "not permitted", "not authorized", "not authorised"} {
			if strings.Contains(message, marker) {
				return "forbidden", fmt.Errorf("permission denied reading space settings")
			}
		}
		return "error", fmt.Errorf("space RPC failed (code %d)", envelope.Error.Code)
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return "unknown", fmt.Errorf("space RPC result is absent")
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		return "error", fmt.Errorf("invalid space RPC result")
	}
	return "ok", nil
}

func handleGetSpace(client *ConfluenceClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := getArguments(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		key, ok := args["spaceKey"].(string)
		if !ok || strings.TrimSpace(key) == "" || strings.Contains(key, "/") || strings.Contains(key, "..") {
			return mcp.NewToolResultError("spaceKey must be nonempty and contain neither '/' nor '..'"), nil
		}
		include := false
		if raw, present := args["includePermissions"]; present {
			include, ok = raw.(bool)
			if !ok {
				return mcp.NewToolResultError("includePermissions must be a boolean"), nil
			}
		}
		var metadata spaceMetadata
		if err := client.getJSON(ctx, "/space/"+url.PathEscape(key), url.Values{"expand": {"homepage"}}, &metadata); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("error reading space: %v", err)), nil
		}
		result := struct {
			spaceMetadata
			Permissions       spaceAccess     `json:"permissions"`
			CurrentOperations spaceOperations `json:"currentUserOperations"`
			Complete          bool            `json:"complete"`
			Warnings          []string        `json:"warnings"`
		}{
			spaceMetadata:     metadata,
			Permissions:       spaceAccess{Status: "not_requested", Assignments: []spaceAssignment{}, AnonymousAccess: "unknown", Source: "legacy-json-rpc/getSpacePermissionSets"},
			CurrentOperations: spaceOperations{Status: "not_requested", Values: []string{}, Source: "legacy-json-rpc/getPermissions"},
			Complete:          metadata.ID != 0 && metadata.Key != "" && metadata.Name != "" && metadata.Type != "" && len(metadata.Homepage) != 0,
			Warnings:          []string{},
		}
		if !result.Complete {
			result.Warnings = append(result.Warnings, "Space metadata is incomplete.")
		}
		if include {
			var sets []struct {
				Type        string `json:"type"`
				Permissions *[]struct {
					Type      string `json:"type"`
					UserName  string `json:"userName"`
					GroupName string `json:"groupName"`
				} `json:"spacePermissions"`
			}
			status, rpcErr := client.spaceRPC(ctx, "getSpacePermissionSets", key, &sets)
			result.Permissions.Status = status
			if rpcErr != nil {
				result.Warnings = append(result.Warnings, "Space permission assignments unavailable: "+rpcErr.Error())
			} else {
				result.Permissions.AssignmentsComplete = true
				for _, set := range sets {
					if set.Type == "" || set.Permissions == nil {
						result.Permissions.AssignmentsComplete = false
						continue
					}
					for _, permission := range *set.Permissions {
						assignment := spaceAssignment{Type: permission.Type, UserName: permission.UserName, GroupName: permission.GroupName, SubjectType: "unknown"}
						if assignment.Type == "" {
							assignment.Type = set.Type
						}
						switch {
						case assignment.UserName != "" && assignment.GroupName == "":
							assignment.SubjectType = "user"
						case assignment.UserName == "" && assignment.GroupName != "":
							assignment.SubjectType = "group"
						}
						result.Permissions.Assignments = append(result.Permissions.Assignments, assignment)
					}
				}
				if !result.Permissions.AssignmentsComplete {
					result.Permissions.Status = "unknown"
					result.Warnings = append(result.Warnings, "Permission sets are missing required fields; assignments are incomplete.")
				} else if len(result.Permissions.Assignments) == 0 {
					result.Permissions.Status = "empty"
				}
			}
			// 7.9 has separate anonymous/all-authenticated subject types, but the
			// legacy DTO does not establish their lossless representation.
			result.Warnings = append(result.Warnings, "Anonymous access is unknown: legacy RPC special-subject encoding is not verified; empty user/group fields are not proof of anonymous access or its absence.")
			var operations []string
			status, rpcErr = client.spaceRPC(ctx, "getPermissions", key, &operations)
			result.CurrentOperations.Status = status
			if rpcErr != nil {
				result.Warnings = append(result.Warnings, "Current-user space permissions unavailable: "+rpcErr.Error())
			} else {
				result.CurrentOperations.Values = operations
				result.CurrentOperations.Complete = true
				if len(operations) == 0 {
					result.CurrentOperations.Status = "empty"
				}
			}
			result.Complete = false // Anonymous assignment semantics remain unchecked.
		}
		result.Warnings = append(result.Warnings, "Space assignments are not effective access: global access, group membership and page/ancestor restrictions are not evaluated.")
		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError("cannot encode space result"), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
