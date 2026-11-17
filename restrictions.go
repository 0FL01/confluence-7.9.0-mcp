package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const restrictionInterpretation = "Own restrictions do not determine public or effective access: space permissions and ancestor read restrictions also apply. Update restrictions are not inherited."
const restrictionConcurrency = "Granular restriction writes have no transaction or compare-and-swap guarantee. Preflight, checks before deletion and readback cannot prevent concurrent administrator changes. No automatic retry or rollback is performed."

type restrictionPage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type restrictionACL struct {
	Operation string           `json:"operation"`
	Users     []map[string]any `json:"users"`
	Groups    []map[string]any `json:"groups"`
	Complete  bool             `json:"complete"`
	Warnings  []string         `json:"warnings"`
}

type restrictionOwn struct {
	Source restrictionPage `json:"source"`
	Read   restrictionACL  `json:"read"`
	Update restrictionACL  `json:"update"`
}

type restrictionAncestor struct {
	Source restrictionPage `json:"source"`
	Read   restrictionACL  `json:"read"`
}

type restrictionAncestors struct {
	Requested bool                  `json:"requested"`
	Status    string                `json:"status"`
	Complete  bool                  `json:"complete"`
	Sources   []restrictionAncestor `json:"sources"`
	Warnings  []string              `json:"warnings"`
}

type restrictionSnapshot struct {
	ContentID      string               `json:"contentId"`
	Title          string               `json:"title"`
	Own            restrictionOwn       `json:"own"`
	Ancestors      restrictionAncestors `json:"ancestors"`
	Complete       bool                 `json:"complete"`
	Warnings       []string             `json:"warnings"`
	Interpretation string               `json:"interpretation"`
}

type restrictionSubjectPage struct {
	Results *[]map[string]any `json:"results"`
	Start   *int              `json:"start"`
	Limit   *int              `json:"limit"`
	Size    *int              `json:"size"`
}

// Do not expose server bodies, decode errors containing input, or transport URLs.
func restrictionRequestError(err error) string {
	var status *apiStatusError
	if errors.As(err, &status) {
		return fmt.Sprintf("API returned HTTP %d", status.StatusCode)
	}
	if errors.Is(err, context.Canceled) {
		return "request canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out"
	}
	return "request failed or returned invalid data"
}

func validRestrictionID(id string) bool {
	return strings.TrimSpace(id) != "" && !strings.Contains(id, "/") && !strings.Contains(id, "..")
}

func restrictionBool(args map[string]any, name string, fallback bool) (bool, error) {
	raw, present := args[name]
	if !present {
		return fallback, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return value, nil
}

// Server 7.9.0 omits next links even when more subjects exist. Track each
// subject list's returned effective limit independently, including an extra
// request after an exactly full page. Never infer completeness from next links.
func readRestrictionACL(ctx context.Context, client *ConfluenceClient, id, operation string) restrictionACL {
	acl := restrictionACL{Operation: operation, Users: []map[string]any{}, Groups: []map[string]any{}, Warnings: []string{}}
	next := [2]int{0, 0}
	done := [2]bool{}
	seen := [2]map[string]bool{{}, {}}
	for {
		start := next[0]
		if done[0] || (!done[1] && next[1] < start) {
			start = next[1]
		}
		var response struct {
			Operation    string `json:"operation"`
			Restrictions struct {
				User  restrictionSubjectPage `json:"user"`
				Group restrictionSubjectPage `json:"group"`
			} `json:"restrictions"`
		}
		query := url.Values{"start": {fmt.Sprint(start)}, "limit": {"200"}}
		err := client.getJSON(ctx, "/content/"+url.PathEscape(id)+"/restriction/byOperation/"+operation, query, &response)
		if err != nil {
			acl.Warnings = append(acl.Warnings, restrictionRequestError(err))
			return acl
		}
		if response.Operation != operation {
			acl.Warnings = append(acl.Warnings, "Missing or mismatched restriction operation")
			return acl
		}
		pages := [2]restrictionSubjectPage{response.Restrictions.User, response.Restrictions.Group}
		for i, page := range pages {
			if done[i] || next[i] != start {
				continue
			}
			if page.Results == nil || page.Start == nil || page.Limit == nil || page.Size == nil ||
				*page.Start != start || *page.Limit <= 0 || *page.Size != len(*page.Results) || *page.Size > *page.Limit {
				acl.Warnings = append(acl.Warnings, "Missing or inconsistent subject pagination data")
				return acl
			}
			key := "userKey"
			if i == 1 {
				key = "name"
			}
			for _, subject := range *page.Results {
				identity, ok := subject[key].(string)
				if !ok || strings.TrimSpace(identity) == "" || seen[i][identity] {
					acl.Warnings = append(acl.Warnings, "Missing stable subject identity or repeated subject; pagination is incomplete")
					return acl
				}
				seen[i][identity] = true
				if i == 0 {
					acl.Users = append(acl.Users, subject)
				} else {
					acl.Groups = append(acl.Groups, subject)
				}
			}
			done[i] = *page.Size < *page.Limit
			next[i] = start + *page.Limit
			if !done[i] && next[i] <= start {
				acl.Warnings = append(acl.Warnings, "Subject pagination did not advance")
				return acl
			}
		}
		if done[0] && done[1] {
			acl.Complete = true
			return acl
		}
	}
}

func readRestrictionSnapshot(ctx context.Context, client *ConfluenceClient, id string, ancestors bool) (*restrictionSnapshot, error) {
	var metadata struct {
		ID        string             `json:"id"`
		Title     *string            `json:"title"`
		Type      string             `json:"type"`
		Ancestors *[]restrictionPage `json:"ancestors"`
	}
	query := url.Values{}
	if ancestors {
		query.Set("expand", "ancestors")
	}
	if err := client.getJSON(ctx, "/content/"+url.PathEscape(id), query, &metadata); err != nil {
		return nil, fmt.Errorf("target page unavailable: %s", restrictionRequestError(err))
	}
	if metadata.ID != id || metadata.Title == nil || metadata.Type != "page" {
		return nil, fmt.Errorf("target must be a page with matching id and title metadata")
	}
	result := &restrictionSnapshot{
		ContentID: id, Title: *metadata.Title, Warnings: []string{}, Interpretation: restrictionInterpretation,
		Ancestors: restrictionAncestors{Requested: ancestors, Status: "not-requested", Sources: []restrictionAncestor{}, Warnings: []string{}},
	}
	result.Own = restrictionOwn{
		Source: restrictionPage{ID: id, Title: *metadata.Title},
		Read:   readRestrictionACL(ctx, client, id, "read"), Update: readRestrictionACL(ctx, client, id, "update"),
	}
	result.Complete = result.Own.Read.Complete && result.Own.Update.Complete
	if !result.Complete {
		result.Warnings = append(result.Warnings, "Own restriction data is incomplete; see each operation's warnings")
	}
	if ancestors {
		result.Ancestors.Status = "complete"
		result.Ancestors.Complete = true
		if metadata.Ancestors == nil {
			result.Ancestors.Complete = false
			result.Ancestors.Warnings = append(result.Ancestors.Warnings, "Ancestor metadata missing")
		} else {
			seen := map[string]bool{id: true}
			for _, page := range *metadata.Ancestors {
				if !validRestrictionID(page.ID) || page.Title == "" || seen[page.ID] {
					result.Ancestors.Complete = false
					result.Ancestors.Warnings = append(result.Ancestors.Warnings, "Invalid or repeated ancestor metadata")
					continue
				}
				seen[page.ID] = true
				acl := readRestrictionACL(ctx, client, page.ID, "read")
				result.Ancestors.Sources = append(result.Ancestors.Sources, restrictionAncestor{Source: page, Read: acl})
				result.Ancestors.Complete = result.Ancestors.Complete && acl.Complete
			}
		}
		if !result.Ancestors.Complete {
			result.Ancestors.Status = "incomplete"
			result.Complete = false
			result.Warnings = append(result.Warnings, "Ancestor read restriction data is incomplete")
		}
	}
	return result, nil
}

func restrictionResult(value any, isError bool) *mcp.CallToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return mcp.NewToolResultError("Could not encode restriction result")
	}
	result := mcp.NewToolResultText(string(data))
	result.IsError = isError
	return result
}

func handleGetRestrictions(client *ConfluenceClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := getArguments(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		id, ok := args["contentId"].(string)
		if !ok || !validRestrictionID(id) {
			return mcp.NewToolResultError("contentId must be a nonempty valid page id"), nil
		}
		ancestors, err := restrictionBool(args, "includeAncestors", false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, err := readRestrictionSnapshot(ctx, client, id, ancestors)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return restrictionResult(result, false), nil
	}
}

type restrictionAction struct {
	Action          string `json:"action"`
	SubjectType     string `json:"subjectType"`
	Subject         string `json:"subject"`
	Status          string `json:"status"`
	Detail          string `json:"detail,omitempty"`
	ReadbackMatches *bool  `json:"readbackMatches,omitempty"`
}

type restrictionUpdateResult struct {
	ContentID            string               `json:"contentId"`
	Operation            string               `json:"operation"`
	DryRun               bool                 `json:"dryRun"`
	ConfirmationRequired bool                 `json:"confirmationRequired"`
	Actions              []restrictionAction  `json:"actions"`
	Noops                []restrictionAction  `json:"noops"`
	Preflight            *restrictionSnapshot `json:"preflight"`
	Readback             *restrictionSnapshot `json:"readback"`
	ReadbackStatus       string               `json:"readbackStatus"`
	Warnings             []string             `json:"warnings"`
	Concurrency          string               `json:"concurrency"`
}

func restrictionSubjects(args map[string]any, name string) ([]string, error) {
	raw, exists := args[name]
	if !exists {
		return nil, nil
	}
	var values []any
	switch array := raw.(type) {
	case []any:
		values = array
	case []string:
		for _, item := range array {
			values = append(values, item)
		}
	default:
		return nil, fmt.Errorf("%s must be an array of nonempty strings", name)
	}
	result := []string{}
	seen := map[string]bool{}
	for _, raw := range values {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s must contain only nonempty strings", name)
		}
		// A group is one path segment. Dot segments cannot be represented safely
		// by URL path joining, even after PathEscape.
		if strings.HasSuffix(name, "Groups") && (value == "." || value == "..") {
			return nil, fmt.Errorf("%s contains an invalid group name", name)
		}
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result, nil
}

func restrictionHas(acl restrictionACL, subjectType, subject string) bool {
	list, key := acl.Users, "userKey"
	if subjectType == "group" {
		list, key = acl.Groups, "name"
	}
	for _, entry := range list {
		if entry[key] == subject {
			return true
		}
	}
	return false
}

func selectedRestrictionACL(snapshot *restrictionSnapshot, operation string) restrictionACL {
	if operation == "read" {
		return snapshot.Own.Read
	}
	return snapshot.Own.Update
}

func handleUpdateRestrictions(client *ConfluenceClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := getArguments(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		id, ok := args["contentId"].(string)
		if !ok || !validRestrictionID(id) {
			return mcp.NewToolResultError("contentId must be a nonempty valid page id"), nil
		}
		operation, ok := args["operation"].(string)
		if !ok || (operation != "read" && operation != "update") {
			return mcp.NewToolResultError("operation must be read or update"), nil
		}
		dryRun, err := restrictionBool(args, "dryRun", true)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		confirm, err := restrictionBool(args, "confirmRemoveLastRestriction", false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		names := []string{"addUsers", "addGroups", "removeUsers", "removeGroups"}
		deltas := make([][]string, len(names))
		for i, name := range names {
			deltas[i], err = restrictionSubjects(args, name)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}
		for i := 0; i < 2; i++ {
			added := map[string]bool{}
			for _, subject := range deltas[i] {
				added[subject] = true
			}
			for _, subject := range deltas[i+2] {
				if added[subject] {
					return mcp.NewToolResultError("The same subject cannot be both added and removed"), nil
				}
			}
		}
		preflight, err := readRestrictionSnapshot(ctx, client, id, false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result := restrictionUpdateResult{
			ContentID: id, Operation: operation, DryRun: dryRun, Preflight: preflight,
			Actions: []restrictionAction{}, Noops: []restrictionAction{}, ReadbackStatus: "not-requested", Warnings: []string{}, Concurrency: restrictionConcurrency,
		}
		if !preflight.Complete {
			result.Warnings = append(result.Warnings, "Complete own read and update restrictions are required before planning or writing")
			return restrictionResult(result, true), nil
		}
		acl := selectedRestrictionACL(preflight, operation)
		predicted := len(acl.Users) + len(acl.Groups)
		removals := 0
		for i, subjects := range deltas {
			kind, action := "user", "add"
			if i%2 == 1 {
				kind = "group"
			}
			if i >= 2 {
				action = "remove"
			}
			for _, subject := range subjects {
				item := restrictionAction{Action: action, SubjectType: kind, Subject: subject, Status: "not-attempted"}
				present := restrictionHas(acl, kind, subject)
				if (action == "add" && present) || (action == "remove" && !present) {
					item.Status = "no-op"
					result.Noops = append(result.Noops, item)
					continue
				}
				result.Actions = append(result.Actions, item)
				if action == "add" {
					predicted++
				} else {
					predicted--
					removals++
				}
			}
		}
		result.ConfirmationRequired = removals > 0 && predicted == 0 && !confirm
		if result.ConfirmationRequired {
			result.Warnings = append(result.Warnings, "Removing the last combined user/group restriction requires confirmRemoveLastRestriction=true")
		}
		if dryRun || result.ConfirmationRequired || len(result.Actions) == 0 {
			return restrictionResult(result, !dryRun && result.ConfirmationRequired), nil
		}
		failed, attempted := false, false
		accepted := make([]bool, len(result.Actions))
		for i := range result.Actions {
			action := &result.Actions[i]
			if action.Action == "remove" && !confirm {
				current := readRestrictionACL(ctx, client, id, operation)
				if !current.Complete {
					action.Status, action.Detail = "failed", "Cannot safely check current restrictions before deletion"
					failed = true
					break
				}
				if !restrictionHas(current, action.SubjectType, action.Subject) {
					action.Detail = "Subject already absent at deletion check"
					accepted[i] = true
					continue
				}
				if len(current.Users)+len(current.Groups) <= 1 {
					action.Status, action.Detail = "failed", "Current restrictions changed; last removal requires explicit confirmation"
					result.ConfirmationRequired = true
					failed = true
					break
				}
			}
			method := http.MethodPut
			if action.Action == "remove" {
				method = http.MethodDelete
			}
			path := "/rest/experimental/content/" + url.PathEscape(id) + "/restriction/byOperation/" + operation
			query := url.Values{}
			if action.SubjectType == "user" {
				path += "/user"
				query.Set("key", action.Subject)
			} else {
				path += "/group/" + url.PathEscape(action.Subject)
			}
			attempted = true
			resp, err := client.executeSiteRequest(ctx, method, path, query, nil, nil)
			if err != nil {
				action.Status, action.Detail = "unknown", "Write outcome unknown: "+restrictionRequestError(err)
				failed = true
				break
			}
			status := resp.StatusCode
			if status < 200 || status >= 300 {
				err = responseStatusError(resp)
			}
			_ = resp.Body.Close()
			if err != nil {
				action.Status = "failed"
				if status >= 500 || status == http.StatusRequestTimeout {
					action.Status = "unknown"
				}
				action.Detail = restrictionRequestError(err)
				failed = true
				break
			}
			action.Status = "unknown" // HTTP acceptance alone does not prove the final ACL.
			accepted[i] = true
		}
		// Read back both own operations and page visibility, including after partial
		// or ambiguous writes. Losing our own read access must not look successful.
		if attempted || !failed {
			result.Readback, err = readRestrictionSnapshot(ctx, client, id, false)
			if err != nil {
				result.ReadbackStatus = "failed"
				result.Warnings = append(result.Warnings, "Readback failed: "+err.Error())
				failed = true
			} else if !result.Readback.Complete {
				result.ReadbackStatus = "incomplete"
				result.Warnings = append(result.Warnings, "Readback is incomplete; final access is not confirmed")
				failed = true
			} else {
				result.ReadbackStatus = "complete"
			}
		}
		for i := range result.Actions {
			action := &result.Actions[i]
			if result.Readback != nil {
				actual := selectedRestrictionACL(result.Readback, operation)
				if actual.Complete {
					matches := restrictionHas(actual, action.SubjectType, action.Subject) == (action.Action == "add")
					action.ReadbackMatches = &matches
				}
			}
			if accepted[i] {
				if result.ReadbackStatus == "complete" && action.ReadbackMatches != nil && *action.ReadbackMatches {
					action.Status = "confirmed"
				} else {
					action.Status, action.Detail = "unknown", "Final requested state could not be confirmed by complete readback"
					failed = true
				}
			}
		}
		return restrictionResult(result, failed), nil
	}
}
