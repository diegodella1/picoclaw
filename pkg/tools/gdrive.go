package tools

import (
	"context"
	"fmt"
	"io"
	"strings"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type GDriveTool struct {
	opt option.ClientOption
}

func NewGDriveTool(saFile, email string) *GDriveTool {
	opt, err := googleClientOption(saFile, email,
		drive.DriveScope,
	)
	if err != nil {
		return nil
	}
	return &GDriveTool{opt: opt}
}

func (t *GDriveTool) Name() string { return "gdrive" }

func (t *GDriveTool) Description() string {
	return "Google Drive: list, search, read, upload, create folders and share files. Actions: list_files, search, read, upload, create_folder, share."
}

func (t *GDriveTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform: list_files, search, read, upload, create_folder, share",
				"enum":        []string{"list_files", "search", "read", "upload", "create_folder", "share"},
			},
			"file_id": map[string]interface{}{
				"type":        "string",
				"description": "File ID (for read/share actions)",
			},
			"folder_id": map[string]interface{}{
				"type":        "string",
				"description": "Folder ID (for list_files/upload). Use 'root' for root folder",
			},
			"parent_id": map[string]interface{}{
				"type":        "string",
				"description": "Parent folder ID (for create_folder)",
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query (for search action, uses Drive query syntax)",
			},
			"max_results": map[string]interface{}{
				"type":        "number",
				"description": "Maximum results to return (default 20)",
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "File/folder name (for upload/create_folder)",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "File content as text (for upload)",
			},
			"mime_type": map[string]interface{}{
				"type":        "string",
				"description": "MIME type (for upload, default: text/plain)",
			},
			"email": map[string]interface{}{
				"type":        "string",
				"description": "Email to share with (for share action)",
			},
			"role": map[string]interface{}{
				"type":        "string",
				"description": "Sharing role: reader, writer, commenter (default: reader)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *GDriveTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action, _ := args["action"].(string)

	srv, err := drive.NewService(ctx, t.opt)
	if err != nil {
		return ErrorResult(fmt.Sprintf("drive service error: %v", err))
	}

	switch action {
	case "list_files":
		return t.listFiles(srv, args)
	case "search":
		return t.search(srv, args)
	case "read":
		return t.read(srv, args)
	case "upload":
		return t.upload(srv, args)
	case "create_folder":
		return t.createFolder(srv, args)
	case "share":
		return t.share(srv, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown gdrive action: %s", action))
	}
}

func (t *GDriveTool) listFiles(srv *drive.Service, args map[string]interface{}) *ToolResult {
	folderID := "root"
	if v, ok := args["folder_id"].(string); ok && v != "" {
		folderID = v
	}

	maxResults := int64(20)
	if n, ok := args["max_results"].(float64); ok && n > 0 {
		maxResults = int64(n)
	}

	query := fmt.Sprintf("'%s' in parents and trashed = false", folderID)
	list, err := srv.Files.List().
		Q(query).
		PageSize(maxResults).
		Fields("files(id, name, mimeType, size, modifiedTime)").
		OrderBy("modifiedTime desc").
		Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to list files: %v", err))
	}

	if len(list.Files) == 0 {
		return SilentResult("No files found.")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files (%d):\n\n", len(list.Files)))

	for i, f := range list.Files {
		typeLabel := "file"
		if f.MimeType == "application/vnd.google-apps.folder" {
			typeLabel = "folder"
		}
		sb.WriteString(fmt.Sprintf("%d. %s [%s]\n   ID: %s\n   Type: %s\n   Modified: %s\n\n",
			i+1, f.Name, typeLabel, f.Id, f.MimeType, f.ModifiedTime))
	}

	return SilentResult(sb.String())
}

func (t *GDriveTool) search(srv *drive.Service, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return ErrorResult("query is required for search")
	}

	maxResults := int64(20)
	if n, ok := args["max_results"].(float64); ok && n > 0 {
		maxResults = int64(n)
	}

	// Wrap in name contains if it's a simple text search (no operators)
	if !strings.Contains(query, " in ") && !strings.Contains(query, "=") && !strings.Contains(query, "contains") {
		query = fmt.Sprintf("name contains '%s' and trashed = false", query)
	}

	list, err := srv.Files.List().
		Q(query).
		PageSize(maxResults).
		Fields("files(id, name, mimeType, size, modifiedTime)").
		Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("search failed: %v", err))
	}

	if len(list.Files) == 0 {
		return SilentResult("No files found matching the query.")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results (%d):\n\n", len(list.Files)))

	for i, f := range list.Files {
		sb.WriteString(fmt.Sprintf("%d. %s\n   ID: %s\n   Type: %s\n   Modified: %s\n\n",
			i+1, f.Name, f.Id, f.MimeType, f.ModifiedTime))
	}

	return SilentResult(sb.String())
}

func (t *GDriveTool) read(srv *drive.Service, args map[string]interface{}) *ToolResult {
	fileID, _ := args["file_id"].(string)
	if fileID == "" {
		return ErrorResult("file_id is required for read")
	}

	// Get file metadata first to check type
	file, err := srv.Files.Get(fileID).Fields("mimeType, name, size").Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get file: %v", err))
	}

	var content string

	// Google Docs/Sheets/Slides → export as text
	switch file.MimeType {
	case "application/vnd.google-apps.document":
		resp, err := srv.Files.Export(fileID, "text/plain").Download()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to export document: %v", err))
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		content = string(data)

	case "application/vnd.google-apps.spreadsheet":
		resp, err := srv.Files.Export(fileID, "text/csv").Download()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to export spreadsheet: %v", err))
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		content = string(data)

	case "application/vnd.google-apps.presentation":
		resp, err := srv.Files.Export(fileID, "text/plain").Download()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to export presentation: %v", err))
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		content = string(data)

	default:
		// Regular file → download
		if file.Size > 1048576 { // 1MB limit for text content
			return ErrorResult(fmt.Sprintf("file too large to read (%d bytes). Max 1MB for text content.", file.Size))
		}
		resp, err := srv.Files.Get(fileID).Download()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to download file: %v", err))
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		content = string(data)
	}

	// Truncate very long content
	if len(content) > 50000 {
		content = content[:50000] + "\n\n[... truncated at 50000 chars]"
	}

	return SilentResult(fmt.Sprintf("File: %s\nType: %s\n\n%s", file.Name, file.MimeType, content))
}

func (t *GDriveTool) upload(srv *drive.Service, args map[string]interface{}) *ToolResult {
	name, _ := args["name"].(string)
	content, _ := args["content"].(string)

	if name == "" || content == "" {
		return ErrorResult("name and content are required for upload")
	}

	mimeType := "text/plain"
	if v, ok := args["mime_type"].(string); ok && v != "" {
		mimeType = v
	}

	file := &drive.File{
		Name:     name,
		MimeType: mimeType,
	}

	if folderID, ok := args["folder_id"].(string); ok && folderID != "" {
		file.Parents = []string{folderID}
	}

	created, err := srv.Files.Create(file).
		Media(strings.NewReader(content)).
		Fields("id, name, webViewLink").
		Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to upload file: %v", err))
	}

	return SilentResult(fmt.Sprintf("File uploaded: %s\nID: %s\nLink: %s", created.Name, created.Id, created.WebViewLink))
}

func (t *GDriveTool) createFolder(srv *drive.Service, args map[string]interface{}) *ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return ErrorResult("name is required for create_folder")
	}

	folder := &drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
	}

	if parentID, ok := args["parent_id"].(string); ok && parentID != "" {
		folder.Parents = []string{parentID}
	}

	created, err := srv.Files.Create(folder).Fields("id, name").Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create folder: %v", err))
	}

	return SilentResult(fmt.Sprintf("Folder created: %s (ID: %s)", created.Name, created.Id))
}

func (t *GDriveTool) share(srv *drive.Service, args map[string]interface{}) *ToolResult {
	fileID, _ := args["file_id"].(string)
	email, _ := args["email"].(string)

	if fileID == "" || email == "" {
		return ErrorResult("file_id and email are required for share")
	}

	role := "reader"
	if v, ok := args["role"].(string); ok && v != "" {
		role = v
	}

	perm := &drive.Permission{
		Type:         "user",
		Role:         role,
		EmailAddress: email,
	}

	_, err := srv.Permissions.Create(fileID, perm).Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to share file: %v", err))
	}

	return SilentResult(fmt.Sprintf("File shared with %s as %s", email, role))
}
