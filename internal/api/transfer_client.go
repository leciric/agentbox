package api

import (
	"context"
	"net/http"
)

// ExportPreview is what this instance has to export.
func (c *Client) ExportPreview(ctx context.Context) (InstanceExportPreview, error) {
	var out InstanceExportPreview
	return out, c.do(ctx, http.MethodGet, "/v1/transfer/export", nil, &out)
}

// ExportInstance starts the job writing an export.
func (c *Client) ExportInstance(ctx context.Context, req InstanceExportRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/transfer/export", req, &out)
}

// InspectImport opens an export and says what importing it would do.
func (c *Client) InspectImport(ctx context.Context, req InstanceImportInspectRequest) (InstanceImportContents, error) {
	var out InstanceImportContents
	return out, c.do(ctx, http.MethodPost, "/v1/transfer/import/inspect", req, &out)
}

// ImportInstance starts the job merging an export into this instance.
func (c *Client) ImportInstance(ctx context.Context, req InstanceImportRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/transfer/import", req, &out)
}
