// Package proxy provides high-performance OpenAI multi-key proxy server
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/config"
	"gpt-load/internal/encryption"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/keypool"
	"gpt-load/internal/models"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/response"
	"gpt-load/internal/services"
	"gpt-load/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// ProxyServer represents the proxy server
type ProxyServer struct {
	keyProvider       *keypool.KeyProvider
	groupManager      *services.GroupManager
	settingsManager   *config.SystemSettingsManager
	channelFactory    *channel.Factory
	requestLogService *services.LogService
	encryptionSvc     encryption.Service
}

// NewProxyServer creates a new proxy server
func NewProxyServer(
	keyProvider *keypool.KeyProvider,
	groupManager *services.GroupManager,
	settingsManager *config.SystemSettingsManager,
	channelFactory *channel.Factory,
	requestLogService *services.LogService,
	encryptionSvc encryption.Service,
) (*ProxyServer, error) {
	return &ProxyServer{
		keyProvider:       keyProvider,
		groupManager:      groupManager,
		settingsManager:   settingsManager,
		channelFactory:    channelFactory,
		requestLogService: requestLogService,
		encryptionSvc:     encryptionSvc,
	}, nil
}

// HandleProxy is the main entry point for proxy requests, refactored based on the stable .bak logic.
func (ps *ProxyServer) HandleProxy(c *gin.Context) {
	startTime := time.Now()
	groupName := c.Param("group_name")

	group, err := ps.groupManager.GetGroupByName(groupName)
	if err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	channelHandler, err := ps.channelFactory.GetChannel(group)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInternalServer, fmt.Sprintf("Failed to get channel for group '%s': %v", groupName, err)))
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logrus.Errorf("Failed to read request body: %v", err)
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, "Failed to read request body"))
		return
	}
	c.Request.Body.Close()

	finalBodyBytes, err := ps.applyParamOverrides(bodyBytes, group)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInternalServer, fmt.Sprintf("Failed to apply parameter overrides: %v", err)))
		return
	}

	finalBodyBytes, err = channelHandler.PrepareRequest(c.Request, finalBodyBytes, group.Name)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, err.Error()))
		return
	}
	streamBody := bodyBytes
	if group.ChannelType == "tavily" {
		streamBody = finalBodyBytes
	}
	isStream := channelHandler.IsStreamRequest(c, streamBody)
	excluded := map[uint]bool(nil)
	if group.ChannelType == "tavily" || group.KeySelectionStrategy == models.KeySelectionFillFirst {
		excluded = make(map[uint]bool)
	}

	ps.executeRequestWithRetry(c, channelHandler, group, finalBodyBytes, isStream, startTime, 0, excluded, nil)
}

// executeRequestWithRetry is the core recursive function for handling requests and retries.
func (ps *ProxyServer) executeRequestWithRetry(
	c *gin.Context,
	channelHandler channel.ChannelProxy,
	group *models.Group,
	bodyBytes []byte,
	isStream bool,
	startTime time.Time,
	retryCount int,
	excluded map[uint]bool,
	selectedKey *models.APIKey,
) {
	cfg := group.EffectiveConfig

	apiKey := selectedKey
	var err error
	if apiKey == nil {
		apiKey, err = ps.keyProvider.SelectKey(group.ID, group.KeySelectionStrategy, excluded)
	}
	if err != nil {
		logrus.Errorf("Failed to select a key for group %s on attempt %d: %v", group.Name, retryCount+1, err)
		response.Error(c, app_errors.NewAPIError(app_errors.ErrNoKeysAvailable, err.Error()))
		ps.logRequest(c, group, nil, startTime, http.StatusServiceUnavailable, err, isStream, "", channelHandler, bodyBytes, models.RequestTypeFinal)
		return
	}

	upstreamURL, err := channelHandler.BuildUpstreamURL(c.Request.URL, group.Name)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInternalServer, fmt.Sprintf("Failed to build upstream URL: %v", err)))
		return
	}

	var ctx context.Context
	var cancel context.CancelFunc
	if isStream {
		ctx, cancel = context.WithCancel(c.Request.Context())
	} else {
		timeout := time.Duration(cfg.RequestTimeout) * time.Second
		ctx, cancel = context.WithTimeout(c.Request.Context(), timeout)
	}
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, c.Request.Method, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		logrus.Errorf("Failed to create upstream request: %v", err)
		response.Error(c, app_errors.ErrInternalServer)
		return
	}
	req.ContentLength = int64(len(bodyBytes))

	req.Header = c.Request.Header.Clone()

	// Clean up client auth key
	req.Header.Del("Authorization")
	req.Header.Del("X-Api-Key")
	req.Header.Del("X-Goog-Api-Key")

	// Apply model redirection
	finalBodyBytes, err := channelHandler.ApplyModelRedirect(req, bodyBytes, group)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, err.Error()))
		ps.logRequest(c, group, apiKey, startTime, http.StatusBadRequest, err, isStream, upstreamURL, channelHandler, bodyBytes, models.RequestTypeFinal)
		return
	}

	// Update request body if it was modified by redirection
	if !bytes.Equal(finalBodyBytes, bodyBytes) {
		req.Body = io.NopCloser(bytes.NewReader(finalBodyBytes))
		req.ContentLength = int64(len(finalBodyBytes))
	}

	channelHandler.ModifyRequest(req, apiKey, group)

	// Apply custom header rules
	if len(group.HeaderRuleList) > 0 {
		headerCtx := utils.NewHeaderVariableContextFromGin(c, group, apiKey)
		utils.ApplyHeaderRules(req, group.HeaderRuleList, headerCtx)
	}

	if group.ChannelType == "tavily" {
		channelHandler.ModifyRequest(req, apiKey, group)
	}
	if isStream {
		req.Header.Set("X-Accel-Buffering", "no")
	}
	failInfrastructure := func(infrastructure *outboundproxy.InfrastructureError) {
		if errors.Is(infrastructure, context.Canceled) || errors.Is(req.Context().Err(), context.Canceled) ||
			(infrastructure.Cause != nil && app_errors.IsIgnorableError(infrastructure.Cause)) {
			ps.logRequest(c, group, apiKey, startTime, 499, infrastructure, isStream, upstreamURL, channelHandler, bodyBytes, models.RequestTypeFinal)
			return
		}
		ps.logRequest(c, group, apiKey, startTime, infrastructure.Status, infrastructure, isStream, upstreamURL, channelHandler, bodyBytes, models.RequestTypeFinal)
		response.Error(c, &app_errors.APIError{HTTPStatus: infrastructure.Status, Code: "OUTBOUND_PROXY_UNAVAILABLE", Message: infrastructure.Error()})
	}
	resp, err := channelHandler.DoRequest(req, apiKey, group, isStream)
	var infrastructure *outboundproxy.InfrastructureError
	if errors.As(err, &infrastructure) {
		failInfrastructure(infrastructure)
		return
	}

	if resp != nil {
		defer resp.Body.Close()
	}

	// Unified error handling for retries.
	// Channels may restrict group failover policy and count key failures separately.
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	retry, countFailure := channelHandler.FailurePolicy(status, err != nil, group)
	if err != nil || retry || countFailure || (group.ChannelType == "tavily" && status >= 400) {
		if err != nil && app_errors.IsIgnorableError(err) {
			logrus.Debugf("Client-side ignorable error for key %s, aborting retries: %v", utils.MaskAPIKey(apiKey.KeyValue), utils.RedactSecret(err.Error(), apiKey.KeyValue))
			err = errors.New(utils.RedactSecret(err.Error(), apiKey.KeyValue))
			ps.logRequest(c, group, apiKey, startTime, 499, err, isStream, upstreamURL, channelHandler, bodyBytes, models.RequestTypeFinal)
			return
		}

		var statusCode int
		var errorMessage string
		var parsedError string

		if err != nil {
			statusCode = 500
			errorMessage = err.Error()
			parsedError = utils.RedactSecret(errorMessage, apiKey.KeyValue)
			logrus.Debugf("Request failed (attempt %d/%d) for key %s: %v", retryCount+1, cfg.MaxRetries, utils.MaskAPIKey(apiKey.KeyValue), utils.RedactSecret(err.Error(), apiKey.KeyValue))
		} else {
			// Retryable upstream response (HTTP status code matched failover policy)
			statusCode = resp.StatusCode
			errorBody, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				if group.StickyProxyEnabled {
					failInfrastructure(&outboundproxy.InfrastructureError{Status: http.StatusBadGateway, Cause: readErr})
					return
				}
				logrus.Errorf("Failed to read error body: %v", readErr)
				errorBody = []byte("Failed to read error body")
			}

			errorBody = handleGzipCompression(resp, errorBody)
			errorMessage = string(errorBody)
			parsedError = utils.RedactSecret(app_errors.ParseUpstreamError(errorBody), apiKey.KeyValue)
			logrus.Debugf("Request failed with status %d (attempt %d/%d) for key %s. Parsed Error: %s", statusCode, retryCount+1, cfg.MaxRetries, utils.MaskAPIKey(apiKey.KeyValue), parsedError)
		}

		// 上游 key 可能出现在错误文本中（如 Gemini 通道将 key 放入 URL query，
		// 传输层错误会把完整 URL 带入 err.Error()），返回客户端和落库前先脱敏
		errorMessage = utils.RedactSecret(errorMessage, apiKey.KeyValue)
		parsedError = utils.RedactSecret(parsedError, apiKey.KeyValue)

		// 使用解析后的错误信息更新密钥状态
		if countFailure {
			ps.keyProvider.UpdateStatus(apiKey, group, false, parsedError)
		}

		// 判断是否为最后一次尝试
		isLastAttempt := !retry || retryCount >= cfg.MaxRetries
		var nextKey *models.APIKey
		if excluded != nil {
			excluded[apiKey.ID] = true
			if !isLastAttempt {
				nextKey, _ = ps.keyProvider.SelectKey(group.ID, group.KeySelectionStrategy, excluded)
				isLastAttempt = nextKey == nil
			}
		}
		requestType := models.RequestTypeRetry
		if isLastAttempt {
			requestType = models.RequestTypeFinal
		}

		ps.logRequest(c, group, apiKey, startTime, statusCode, errors.New(parsedError), isStream, upstreamURL, channelHandler, bodyBytes, requestType)

		// 如果是最后一次尝试，直接返回错误，不再递归
		if isLastAttempt {
			if resp != nil && group.ChannelType == "tavily" {
				for name, values := range resp.Header {
					for _, value := range values {
						c.Writer.Header().Add(name, utils.RedactSecret(value, apiKey.KeyValue))
					}
				}
				// The body has been decoded and may have been redacted.
				c.Writer.Header().Del("Content-Encoding")
				c.Writer.Header().Del("Content-Length")
				c.Data(statusCode, resp.Header.Get("Content-Type"), []byte(errorMessage))
				return
			}
			var errorJSON map[string]any
			if err := json.Unmarshal([]byte(errorMessage), &errorJSON); err == nil {
				c.JSON(statusCode, errorJSON)
			} else {
				response.Error(c, app_errors.NewAPIErrorWithUpstream(statusCode, "UPSTREAM_ERROR", errorMessage))
			}
			return
		}

		ps.executeRequestWithRetry(c, channelHandler, group, bodyBytes, isStream, startTime, retryCount+1, excluded, nextKey)
		return
	}

	logrus.Debugf("Request for group %s succeeded on attempt %d with key %s", group.Name, retryCount+1, utils.MaskAPIKey(apiKey.KeyValue))

	// Check if this is a model list request (needs special handling)
	if shouldInterceptModelList(c.Request.URL.Path, c.Request.Method) {
		ps.handleModelListResponse(c, resp, group, channelHandler)
	} else {
		for key, values := range resp.Header {
			for _, value := range values {
				c.Header(key, value)
			}
		}
		c.Status(resp.StatusCode)

		if isStream {
			ps.handleStreamingResponse(c, resp)
		} else {
			ps.handleNormalResponse(c, resp)
		}
	}

	ps.logRequest(c, group, apiKey, startTime, resp.StatusCode, nil, isStream, upstreamURL, channelHandler, bodyBytes, models.RequestTypeFinal)
}

// logRequest is a helper function to create and record a request log.
func (ps *ProxyServer) logRequest(
	c *gin.Context,
	group *models.Group,
	apiKey *models.APIKey,
	startTime time.Time,
	statusCode int,
	finalError error,
	isStream bool,
	upstreamAddr string,
	channelHandler channel.ChannelProxy,
	bodyBytes []byte,
	requestType string,
) {

	var requestBodyToLog, userAgent string

	if group.EffectiveConfig.EnableRequestBodyLogging {
		requestBodyToLog = utils.TruncateString(string(bodyBytes), 65000)
		userAgent = c.Request.UserAgent()
	}

	duration := time.Since(startTime).Milliseconds()

	logEntry := &models.RequestLog{
		GroupID:      group.ID,
		GroupName:    group.Name,
		IsSuccess:    finalError == nil && statusCode < 400,
		SourceIP:     c.ClientIP(),
		StatusCode:   statusCode,
		RequestPath:  utils.TruncateString(c.Request.URL.String(), 500),
		Duration:     duration,
		UserAgent:    userAgent,
		RequestType:  requestType,
		IsStream:     isStream,
		UpstreamAddr: utils.TruncateString(upstreamAddr, 500),
		RequestBody:  requestBodyToLog,
	}

	if channelHandler != nil && bodyBytes != nil {
		logEntry.Model = channelHandler.ExtractModel(c, bodyBytes)
	}

	if apiKey != nil {
		// 加密密钥值用于日志存储
		encryptedKeyValue, err := ps.encryptionSvc.Encrypt(apiKey.KeyValue)
		if err != nil {
			logrus.WithError(err).Error("Failed to encrypt key value for logging")
			logEntry.KeyValue = "failed-to-encryption"
		} else {
			logEntry.KeyValue = encryptedKeyValue
		}
		// 添加 KeyHash 用于反查
		logEntry.KeyHash = ps.encryptionSvc.Hash(apiKey.KeyValue)
	}

	if finalError != nil {
		logEntry.ErrorMessage = finalError.Error()
	}

	if err := ps.requestLogService.Record(logEntry); err != nil {
		logrus.Errorf("Failed to record request log: %v", err)
	}
}
