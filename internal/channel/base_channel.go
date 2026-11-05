package channel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gpt-load/internal/httpclient"
	"gpt-load/internal/models"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/types"
	"gpt-load/internal/utils"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

// UpstreamInfo holds the information for a single upstream server, including its weight.
type UpstreamInfo struct {
	URL           *url.URL
	Weight        int
	CurrentWeight int
}

// BaseChannel provides common functionality for channel proxies.
type BaseChannel struct {
	proxyManager       *outboundproxy.Manager
	clientConfig       httpclient.Config
	streamConfig       httpclient.Config
	Name               string
	Upstreams          []UpstreamInfo
	HTTPClient         *http.Client
	StreamClient       *http.Client
	TestModel          string
	ValidationEndpoint string
	upstreamLock       sync.Mutex

	// Cached fields from the group for stale check
	channelType         string
	groupUpstreams      models.JSON
	effectiveConfig     *types.SystemSettings
	modelRedirectRules  models.JSONMap
	modelRedirectStrict bool
}

// getUpstreamURL selects an upstream URL using a smooth weighted round-robin algorithm.
func (b *BaseChannel) getUpstreamURL() *url.URL {
	b.upstreamLock.Lock()
	defer b.upstreamLock.Unlock()

	if len(b.Upstreams) == 0 {
		return nil
	}
	if len(b.Upstreams) == 1 {
		return b.Upstreams[0].URL
	}

	totalWeight := 0
	var best *UpstreamInfo

	for i := range b.Upstreams {
		up := &b.Upstreams[i]
		totalWeight += up.Weight
		up.CurrentWeight += up.Weight

		if best == nil || up.CurrentWeight > best.CurrentWeight {
			best = up
		}
	}

	best.CurrentWeight -= totalWeight
	return best.URL
}

// BuildUpstreamURL constructs the target URL for the upstream service.
func (b *BaseChannel) BuildUpstreamURL(originalURL *url.URL, groupName string) (string, error) {
	base := b.getUpstreamURL()
	if base == nil {
		return "", fmt.Errorf("no upstream URL configured for channel %s", b.Name)
	}

	finalURL := *base
	proxyPrefix := "/proxy/" + groupName
	requestPath := originalURL.Path
	requestPath = strings.TrimPrefix(requestPath, proxyPrefix)

	finalURL.Path = strings.TrimRight(finalURL.Path, "/") + requestPath

	finalURL.RawQuery = originalURL.RawQuery

	return finalURL.String(), nil
}

// IsConfigStale checks if the channel's configuration is stale compared to the provided group.
func (b *BaseChannel) IsConfigStale(group *models.Group) bool {
	if b.channelType != group.ChannelType {
		return true
	}
	if b.TestModel != group.TestModel {
		return true
	}
	if b.ValidationEndpoint != utils.GetValidationEndpoint(group) {
		return true
	}
	if !bytes.Equal(b.groupUpstreams, group.Upstreams) {
		return true
	}
	if !reflect.DeepEqual(b.effectiveConfig, &group.EffectiveConfig) {
		return true
	}
	// Check for model redirect rules changes
	if !reflect.DeepEqual(b.modelRedirectRules, group.ModelRedirectRules) {
		return true
	}
	if b.modelRedirectStrict != group.ModelRedirectStrict {
		return true
	}
	return false
}

// GetHTTPClient returns the client for standard requests.
func (b *BaseChannel) GetHTTPClient() *http.Client {
	return b.HTTPClient
}

// GetStreamClient returns the client for streaming requests.
func (b *BaseChannel) GetStreamClient() *http.Client {
	return b.StreamClient
}

// ApplyModelRedirect applies model redirection based on the group's redirect rules.
func (b *BaseChannel) ApplyModelRedirect(req *http.Request, bodyBytes []byte, group *models.Group) ([]byte, error) {
	if len(group.ModelRedirectMap) == 0 || len(bodyBytes) == 0 {
		return bodyBytes, nil
	}

	var requestData map[string]any
	if err := json.Unmarshal(bodyBytes, &requestData); err != nil {
		return bodyBytes, nil
	}

	modelValue, exists := requestData["model"]
	if !exists {
		return bodyBytes, nil
	}

	model, ok := modelValue.(string)
	if !ok {
		return bodyBytes, nil
	}

	// Direct match without any prefix processing
	if targetModel, found := group.ModelRedirectMap[model]; found {
		requestData["model"] = targetModel

		// Log the redirection for audit
		logrus.WithFields(logrus.Fields{
			"group":          group.Name,
			"original_model": model,
			"target_model":   targetModel,
			"channel":        "json_body",
		}).Debug("Model redirected")

		return json.Marshal(requestData)
	}

	if group.ModelRedirectStrict {
		return nil, fmt.Errorf("model '%s' is not configured in redirect rules", model)
	}

	return bodyBytes, nil
}

// TransformModelList transforms the model list response based on redirect rules.
func (b *BaseChannel) TransformModelList(req *http.Request, bodyBytes []byte, group *models.Group) (map[string]any, error) {
	var response map[string]any
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		logrus.WithError(err).Debug("Failed to parse model list response, returning empty")
		return nil, err
	}

	dataInterface, exists := response["data"]
	if !exists {
		return response, nil
	}

	upstreamModels, ok := dataInterface.([]any)
	if !ok {
		return response, nil
	}

	// Build configured source models list (common logic for both modes)
	configuredModels := buildConfiguredModels(group.ModelRedirectMap)

	// Strict mode: return only configured models (whitelist)
	if group.ModelRedirectStrict {
		response["data"] = configuredModels

		logrus.WithFields(logrus.Fields{
			"group":       group.Name,
			"model_count": len(configuredModels),
			"strict_mode": true,
		}).Debug("Model list returned (strict mode - configured models only)")

		return response, nil
	}

	// Non-strict mode: merge upstream + configured models (upstream priority)
	merged := mergeModelLists(upstreamModels, configuredModels)
	response["data"] = merged

	logrus.WithFields(logrus.Fields{
		"group":            group.Name,
		"upstream_count":   len(upstreamModels),
		"configured_count": len(configuredModels),
		"merged_count":     len(merged),
		"strict_mode":      false,
	}).Debug("Model list merged (non-strict mode)")

	return response, nil
}

// buildConfiguredModels builds a list of models from redirect rules
func buildConfiguredModels(redirectMap map[string]string) []any {
	if len(redirectMap) == 0 {
		return []any{}
	}

	models := make([]any, 0, len(redirectMap))
	for sourceModel := range redirectMap {
		models = append(models, map[string]any{
			"id":       sourceModel,
			"object":   "model",
			"created":  0,
			"owned_by": "system",
		})
	}
	return models
}

// mergeModelLists merges upstream and configured model lists
func mergeModelLists(upstream []any, configured []any) []any {
	// Create set of upstream model IDs
	upstreamIDs := make(map[string]bool)
	for _, item := range upstream {
		if modelObj, ok := item.(map[string]any); ok {
			if modelID, ok := modelObj["id"].(string); ok {
				upstreamIDs[modelID] = true
			}
		}
	}

	// Start with all upstream models
	result := make([]any, len(upstream))
	copy(result, upstream)

	// Add configured models that don't exist in upstream
	for _, item := range configured {
		if modelObj, ok := item.(map[string]any); ok {
			if modelID, ok := modelObj["id"].(string); ok {
				if !upstreamIDs[modelID] {
					result = append(result, item)
				}
			}
		}
	}

	return result
}

func (b *BaseChannel) PrepareRequest(req *http.Request, body []byte, groupName string) ([]byte, error) {
	return body, nil
}
func (b *BaseChannel) FailurePolicy(status int, transportError bool, group *models.Group) (bool, bool) {
	retry := transportError || group.FailoverStatusCodeMatcher.Match(status)
	return retry, retry
}

// DoRequest resolves sticky routing per key without mutating shared transports.
func (b *BaseChannel) DoRequest(req *http.Request, key *models.APIKey, group *models.Group, isStream bool) (*http.Response, error) {
	client := b.HTTPClient
	var proxyID uint
	if isStream {
		client = b.StreamClient
	}
	if group.StickyProxyEnabled {
		req.Header.Del("Proxy-Authorization")
		if b.proxyManager == nil || key == nil {
			return nil, &outboundproxy.InfrastructureError{Status: 503}
		}
		config := b.clientConfig
		if isStream {
			config = b.streamConfig
		}
		var err error
		client, proxyID, err = b.proxyManager.ClientFor(key.KeyHash, config)
		if err != nil {
			return nil, err
		}
	}
	resp, err := client.Do(req)
	if group.StickyProxyEnabled {
		if err != nil {
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			return nil, &outboundproxy.InfrastructureError{Status: 502, ProxyID: proxyID, Cause: err}
		}
		if resp.StatusCode == http.StatusProxyAuthRequired {
			resp.Body.Close()
			return nil, &outboundproxy.InfrastructureError{Status: 502, ProxyID: proxyID}
		}
	}
	return resp, err
}
