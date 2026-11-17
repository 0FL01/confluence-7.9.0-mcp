package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// attachmentMetadata is the Server 7.9 content representation, including the
// attachment-specific metadata returned by both POST and GET.
type attachmentMetadata struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Container struct {
		ID string `json:"id"`
	} `json:"container"`
	Version struct {
		Number int `json:"number"`
	} `json:"version"`
	Metadata struct {
		MediaType string `json:"mediaType"`
	} `json:"metadata"`
	Extensions struct {
		MediaType string `json:"mediaType"`
		FileSize  *int64 `json:"fileSize"`
	} `json:"extensions"`
	Links struct {
		Download string `json:"download"`
	} `json:"_links"`
}

type attachmentList struct {
	Results []attachmentMetadata `json:"results"`
}

type uploadedAttachment struct {
	AttachmentID string `json:"attachmentId"`
	ContentID    string `json:"contentId"`
	FileName     string `json:"fileName"`
	MediaType    string `json:"mediaType"`
	FileSize     int64  `json:"fileSize"`
	Version      int    `json:"version"`
	DownloadURL  string `json:"downloadUrl"`
}

func attachmentIDValid(id string) bool {
	return strings.TrimSpace(id) != "" && !strings.Contains(id, "/") && !strings.Contains(id, "..")
}

func handleUploadAttachment(client *ConfluenceClient) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := getArguments(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		values := make(map[string]string, 5)
		for _, name := range []string{"contentId", "filePath", "fileName", "comment", "attachmentId"} {
			raw, present := args[name]
			if !present {
				if name == "contentId" || name == "filePath" {
					return mcp.NewToolResultError(name + " is required"), nil
				}
				continue
			}
			value, ok := raw.(string)
			if !ok || (name != "comment" && strings.TrimSpace(value) == "") {
				return mcp.NewToolResultError(name + " must be a string (non-empty except for comment)"), nil
			}
			values[name] = value
		}
		pageID, attachmentID := values["contentId"], values["attachmentId"]
		if !attachmentIDValid(pageID) || (attachmentID != "" && !attachmentIDValid(attachmentID)) {
			return mcp.NewToolResultError("contentId and attachmentId must be single IDs without '/' or '..'"), nil
		}
		filePath := values["filePath"]
		if !filepath.IsAbs(filePath) {
			return mcp.NewToolResultError("filePath must be absolute"), nil
		}
		// Stat before opening so a FIFO or device cannot block this handler.
		info, err := os.Stat(filePath)
		if errors.Is(err, os.ErrNotExist) {
			return mcp.NewToolResultError("local file not found"), nil
		}
		if errors.Is(err, os.ErrPermission) {
			return mcp.NewToolResultError("permission denied reading local file"), nil
		}
		if err != nil || !info.Mode().IsRegular() {
			return mcp.NewToolResultError("filePath must identify a readable regular file"), nil
		}
		file, err := os.Open(filePath)
		if err != nil {
			return mcp.NewToolResultError("cannot open filePath for reading"), nil
		}
		defer func() { _ = file.Close() }()
		info, err = file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return mcp.NewToolResultError("filePath must identify a readable regular file"), nil
		}
		filename := values["fileName"]
		if filename == "" {
			filename = filepath.Base(filePath)
		}
		if filename == "." || filename == ".." || strings.ContainsAny(filename, "/\\\r\n\x00") {
			return mcp.NewToolResultError("fileName must be a basename without path separators or control characters"), nil
		}
		if attachmentID != "" {
			metadata, err := client.readAttachment(ctx, attachmentID)
			if err != nil {
				return mcp.NewToolResultError("cannot verify attachment before update: " + err.Error()), nil
			}
			if metadata.ID != attachmentID || metadata.Type != "attachment" || metadata.Container.ID != pageID || metadata.Version.Number < 1 {
				return mcp.NewToolResultError("attachment metadata does not verify its ID, attachment type, page membership and version; no upload attempted"), nil
			}
		}
		matches, err := client.findAttachments(ctx, pageID, filename)
		if err != nil {
			return mcp.NewToolResultError("cannot check attachment filename: " + err.Error()), nil
		}
		for _, existing := range matches {
			if attachmentID == "" || existing.ID != attachmentID {
				return mcp.NewToolResultError("attachment filename already exists on this page; use its explicit attachmentId to update it"), nil
			}
		}
		mediaType := attachmentMediaType(file, filename)
		metadata, err := client.uploadAttachment(ctx, pageID, attachmentID, filename, mediaType, values["comment"], file)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// A successful write may omit expansions. Only read metadata now: never
		// replay a POST to obtain a more complete response.
		if !metadata.complete(pageID, attachmentID) {
			id := attachmentID
			if id == "" {
				id = metadata.ID
			}
			if id == "" {
				matches, err = client.findAttachments(ctx, pageID, filename)
				if err == nil && len(matches) == 1 {
					id = matches[0].ID
				}
			}
			if err == nil && attachmentIDValid(id) {
				metadata, err = client.readAttachment(ctx, id)
			}
			if err != nil || !metadata.complete(pageID, attachmentID) {
				return mcp.NewToolResultError("upload accepted by server, but complete attachment metadata could not be verified; inspect the page before any further upload"), nil
			}
		}
		downloadURL, err := client.attachmentDownloadURL(metadata.Links.Download)
		if err != nil {
			return mcp.NewToolResultError("upload accepted by server, but its download link could not be resolved; inspect the attachment before any further upload"), nil
		}
		actualMediaType := metadata.Metadata.MediaType
		if actualMediaType == "" {
			actualMediaType = metadata.Extensions.MediaType
		}
		result := uploadedAttachment{
			AttachmentID: metadata.ID, ContentID: metadata.Container.ID,
			FileName: metadata.Title, MediaType: actualMediaType,
			FileSize: *metadata.Extensions.FileSize, Version: metadata.Version.Number,
			DownloadURL: downloadURL,
		}
		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError("upload accepted, but result encoding failed"), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func (a attachmentMetadata) complete(pageID, attachmentID string) bool {
	return attachmentIDValid(a.ID) && (attachmentID == "" || a.ID == attachmentID) &&
		a.Type == "attachment" && a.Container.ID == pageID && a.Title != "" &&
		a.Version.Number > 0 && a.Extensions.FileSize != nil && *a.Extensions.FileSize >= 0 &&
		(a.Metadata.MediaType != "" || a.Extensions.MediaType != "") && a.Links.Download != ""
}

func (c *ConfluenceClient) readAttachment(ctx context.Context, id string) (attachmentMetadata, error) {
	var metadata attachmentMetadata
	err := c.getJSON(ctx, "/content/"+url.PathEscape(id), url.Values{"expand": {"container,version"}}, &metadata)
	return metadata, err
}

func (c *ConfluenceClient) findAttachments(ctx context.Context, pageID, filename string) ([]attachmentMetadata, error) {
	var matches []attachmentMetadata
	for start := 0; ; {
		var page attachmentList
		query := url.Values{"filename": {filename}, "start": {strconv.Itoa(start)}, "limit": {"100"}, "expand": {"container,version"}}
		if err := c.getJSON(ctx, "/content/"+url.PathEscape(pageID)+"/child/attachment", query, &page); err != nil {
			return nil, err
		}
		if page.Results == nil {
			return nil, fmt.Errorf("attachment listing is incomplete")
		}
		if len(page.Results) == 0 {
			return matches, nil
		}
		for _, attachment := range page.Results {
			if !attachmentIDValid(attachment.ID) || attachment.Title != filename {
				return nil, fmt.Errorf("attachment filename lookup returned unverifiable metadata")
			}
			for _, prior := range matches {
				if prior.ID == attachment.ID {
					return nil, fmt.Errorf("attachment listing did not advance")
				}
			}
			matches = append(matches, attachment)
		}
		start += len(page.Results)
	}
}

func attachmentMediaType(file *os.File, filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".png":
		return "image/png"
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".jsonl", ".ndjson":
		return "application/x-ndjson"
	}
	if mediaType := mime.TypeByExtension(filepath.Ext(filename)); mediaType != "" {
		return mediaType
	}
	var prefix [512]byte
	n, _ := file.ReadAt(prefix[:], 0)
	return http.DetectContentType(prefix[:n])
}

func (c *ConfluenceClient) uploadAttachment(ctx context.Context, pageID, attachmentID, filename, mediaType, comment string, file *os.File) (attachmentMetadata, error) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	done := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() {
		_ = reader.CloseWithError(ctx.Err())
		_ = file.Close()
	})
	defer stop()
	go func() {
		header := make(textproto.MIMEHeader)
		// Match multipart.CreateFormFile's quoted filename encoding. Older
		// server multipart parsers need filename, rather than RFC 2231 filename*.
		escapedFilename := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(filename)
		header.Set("Content-Disposition", `form-data; name="file"; filename="`+escapedFilename+`"`)
		header.Set("Content-Type", mediaType)
		part, err := multipartWriter.CreatePart(header)
		if err == nil {
			_, err = io.Copy(part, file)
		}
		if err == nil && comment != "" {
			err = multipartWriter.WriteField("comment", comment)
		}
		if err == nil {
			err = multipartWriter.Close()
		}
		_ = writer.CloseWithError(err)
		done <- err
	}()
	path := "/rest/api/content/" + url.PathEscape(pageID) + "/child/attachment"
	query := url.Values{"allowDuplicated": {"false"}}
	if attachmentID != "" {
		path += "/" + url.PathEscape(attachmentID) + "/data"
		query = nil
	}
	headers := http.Header{"Content-Type": {multipartWriter.FormDataContentType()}, "X-Atlassian-Token": {"nocheck"}}
	resp, err := c.executeSiteRequest(ctx, http.MethodPost, path, query, reader, headers)
	_ = reader.Close()
	streamErr := <-done
	if err != nil {
		return attachmentMetadata{}, fmt.Errorf("upload outcome unknown: %w; the server may have written the attachment; inspect it before retrying", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return attachmentMetadata{}, fmt.Errorf("upload returned %w; inspect the attachment before retrying", responseStatusError(resp))
	}
	if streamErr != nil {
		return attachmentMetadata{}, fmt.Errorf("upload outcome unknown: file streaming did not finish; the server may have written the attachment; inspect it before retrying")
	}
	var result struct {
		attachmentMetadata
		Results []attachmentMetadata `json:"results"`
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&result); err != nil {
		return attachmentMetadata{}, fmt.Errorf("upload outcome unknown: invalid or unreadable server response; the server may have written the attachment; inspect it before retrying")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return attachmentMetadata{}, fmt.Errorf("upload outcome unknown: incomplete or invalid server response; the server may have written the attachment; inspect it before retrying")
	}
	if len(result.Results) == 1 {
		return result.Results[0], nil
	}
	if len(result.Results) > 1 {
		return attachmentMetadata{}, fmt.Errorf("upload accepted but returned multiple attachments; inspect the page before any further upload")
	}
	return result.attachmentMetadata, nil
}

func (c *ConfluenceClient) attachmentDownloadURL(download string) (string, error) {
	base, err := url.Parse(c.config.BaseURL)
	if err != nil {
		return "", err
	}
	link, err := url.Parse(download)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimSuffix(strings.TrimRight(base.Path, "/"), "/rest/api") + "/"
	base.RawPath, base.RawQuery, base.Fragment, base.User = "", "", "", nil
	// Confluence download links are site-relative (even when starting with /).
	if !link.IsAbs() && link.Host == "" {
		contextPath := strings.TrimRight(base.Path, "/")
		if contextPath != "" && strings.HasPrefix(link.Path, contextPath+"/") {
			link.Path = strings.TrimPrefix(link.Path, contextPath)
		}
		link.Path = strings.TrimPrefix(link.Path, "/")
		link.RawPath = ""
	}
	resolved := base.ResolveReference(link)
	if resolved.Scheme != base.Scheme || resolved.Host != base.Host || resolved.User != nil {
		return "", fmt.Errorf("invalid attachment download origin")
	}
	return resolved.String(), nil
}
