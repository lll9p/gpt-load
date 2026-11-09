package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"

	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/response"

	"github.com/gin-gonic/gin"
)

type OutboundProxyCreateRequest struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

type OutboundProxyUpdateRequest struct {
	Name    *string `json:"name"`
	URL     *string `json:"url"`
	Enabled *bool   `json:"enabled"`
}

func validateID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, "invalid proxy ID"))
		return 0, false
	}
	return uint(id), true
}

func handleOutboundProxyError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var apiErr *app_errors.APIError
	if errors.As(err, &apiErr) {
		response.Error(c, apiErr)
	} else {
		response.Error(c, app_errors.ErrInternalServer)
	}
	return true
}

func (s *Server) ListOutboundProxies(c *gin.Context) {
	// Manager.List returns decrypted copies, never the persisted ciphertext.
	proxies, err := s.ProxyManager.List()
	if handleOutboundProxyError(c, err) {
		return
	}
	response.Success(c, proxies)
}

func (s *Server) CreateOutboundProxy(c *gin.Context) {
	var req OutboundProxyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	proxy, err := s.ProxyManager.Create(req.Name, req.URL, enabled)
	if handleOutboundProxyError(c, err) {
		return
	}
	response.Success(c, proxy)
}

func batchValidationError(c *gin.Context, index int) {
	apiErr := app_errors.ErrValidation
	c.JSON(apiErr.HTTPStatus, struct {
		response.ErrorResponse
		Data struct {
			Index int `json:"index"`
		} `json:"data"`
	}{
		ErrorResponse: response.ErrorResponse{Code: apiErr.Code, Message: apiErr.Message},
		Data: struct {
			Index int `json:"index"`
		}{Index: index},
	})
}

func (s *Server) CreateOutboundProxyBatch(c *gin.Context) {
	var req struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.ErrInvalidJSON)
		return
	}
	if len(req.Items) == 0 || len(req.Items) > 500 {
		response.Error(c, app_errors.ErrValidation)
		return
	}
	items := make([]outboundproxy.BatchItem, len(req.Items))
	for i, raw := range req.Items {
		var item struct {
			URL     *string         `json:"url"`
			Enabled json.RawMessage `json:"enabled"`
		}
		if err := json.Unmarshal(raw, &item); err != nil || item.URL == nil {
			batchValidationError(c, i)
			return
		}
		items[i].URL = *item.URL
		if len(item.Enabled) != 0 {
			var enabled bool
			if err := json.Unmarshal(item.Enabled, &enabled); err != nil || bytes.Equal(bytes.TrimSpace(item.Enabled), []byte("null")) {
				batchValidationError(c, i)
				return
			}
			items[i].Enabled = &enabled
		}
	}
	result, err := s.ProxyManager.CreateBatch(items)
	var indexed *outboundproxy.IndexedValidationError
	if errors.As(err, &indexed) {
		batchValidationError(c, indexed.Index)
		return
	}
	if handleOutboundProxyError(c, err) {
		return
	}
	response.Success(c, result)
}

func (s *Server) UpdateOutboundProxy(c *gin.Context) {
	id, ok := validateID(c)
	if !ok {
		return
	}
	var req OutboundProxyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}
	proxy, err := s.ProxyManager.Update(id, req.Name, req.URL, req.Enabled)
	if handleOutboundProxyError(c, err) {
		return
	}
	response.Success(c, proxy)
}

func (s *Server) DeleteOutboundProxy(c *gin.Context) {
	id, ok := validateID(c)
	if !ok {
		return
	}
	if handleOutboundProxyError(c, s.ProxyManager.Delete(id)) {
		return
	}
	response.Success(c, nil)
}
