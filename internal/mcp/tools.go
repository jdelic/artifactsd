package mcp

import (
	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools adds the ten SPEC §16 tools to s.MCP. Each tool calls the
// management API through s.Client — the same client the CLI uses — and maps
// apiclient errors via MapError (design/11 §2, §5).
func registerTools(s *Server) {
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_create_collection",
		Description: "Create a collection: the top-level namespace for a published artifact tree.",
	}, s.createCollection)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_put_text",
		Description: "Upload a small text artifact (e.g. an HTML report or JSON file) inline.",
	}, s.putText)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_begin_upload",
		Description: "Start a large or binary upload: returns a direct HTTP PUT URL the agent sends bytes to out of band.",
	}, s.beginUpload)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_get_metadata",
		Description: "Read a file or folder's own and effective metadata.",
	}, s.getMetadata)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_set_metadata",
		Description: "Merge a metadata patch (access/crawl/expiry/content headers) onto a file or folder.",
	}, s.setMetadata)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_delete",
		Description: "Delete a file.",
	}, s.deleteArtifact)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_list_tree",
		Description: "List a collection's files and folders (the admin view: all visibility levels, unfiltered by crawl policy).",
	}, s.listTree)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_get_public_url",
		Description: "Compose the public /c/... URL for a collection or file, without a server round trip.",
	}, s.getPublicURL)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_create_share",
		Description: "Create a hard-to-guess share URL for a collection, folder, or file, with optional bearer-token auth and expiry.",
	}, s.createShare)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_get_crawl_tree",
		Description: "Enumerate a collection's policy-filtered crawl tree: only the crawlable/indexable objects an agent may see (SPEC §10.5).",
	}, s.getCrawlTree)

	// Upload request links (design/15 §7).
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_create_upload_request",
		Description: "Create an upload request link: a shareable capability URL (/r/{id}) that lets third parties upload files without API credentials.",
	}, s.createUploadRequest)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_list_upload_requests",
		Description: "List all upload request links and their current status.",
	}, s.listUploadRequests)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_get_upload_request",
		Description: "Get the status and details of one upload request link by its request_id.",
	}, s.getUploadRequest)
	gosdk.AddTool(s.MCP, &gosdk.Tool{
		Name:        "artifact_revoke_upload_request",
		Description: "Revoke an upload request link, permanently preventing further uploads.",
	}, s.revokeUploadRequest)
}
