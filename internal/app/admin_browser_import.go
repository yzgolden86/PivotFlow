package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func readBrowserImportRequest(c *gin.Context, target any) error {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, browserImportMaxBytes+1))
	if err != nil {
		return previewFailure("", "browser_import_invalid_document")
	}
	if len(raw) > browserImportMaxBytes {
		return previewFailure("", "browser_import_too_large")
	}
	if _, err = decodeBrowserImportJSON(raw); err != nil {
		return err
	}
	if err = json.Unmarshal(raw, target); err != nil {
		return previewFailure("", "browser_import_invalid_document")
	}
	return nil
}

func respondBrowserImportError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	if strings.Contains(err.Error(), "browser_import_too_large") {
		status = http.StatusRequestEntityTooLarge
	} else if err.Error() == "request_failed" {
		status = http.StatusInternalServerError
	}
	RespondErrorMsg(c, status, err.Error())
}

func (s *Server) HandleBrowserImportInspect(c *gin.Context) {
	var request struct {
		Document json.RawMessage `json:"document"`
	}
	if err := readBrowserImportRequest(c, &request); err != nil {
		respondBrowserImportError(c, err)
		return
	}
	document, err := decodeBrowserImportDocument(request.Document)
	if err != nil {
		respondBrowserImportError(c, err)
		return
	}
	inspection, err := s.inspectBrowserImport(c.Request.Context(), document)
	if err != nil {
		respondBrowserImportError(c, err)
		return
	}
	RespondJSON(c, http.StatusOK, inspection)
}

func (s *Server) HandleBrowserImportPreview(c *gin.Context) {
	var request browserImportPreviewRequest
	if err := readBrowserImportRequest(c, &request); err != nil {
		respondBrowserImportError(c, err)
		return
	}
	view, err := s.previewBrowserImport(c.Request.Context(), request)
	if err != nil {
		respondBrowserImportError(c, err)
		return
	}
	RespondJSON(c, http.StatusOK, view)
}
