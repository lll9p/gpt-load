package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/utils"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

var ErrValidationInconclusive = errors.New("key validation inconclusive")

type TavilyChannel struct{ *BaseChannel }

func init() { Register("tavily", newTavilyChannel) }

func newTavilyChannel(f *Factory, group *models.Group) (ChannelProxy, error) {
	base, err := f.newBaseChannel("tavily", group)
	if err != nil {
		return nil, err
	}
	return &TavilyChannel{base}, nil
}

func (ch *TavilyChannel) PrepareRequest(req *http.Request, body []byte, groupName string) ([]byte, error) {
	q := req.URL.Query()
	q.Del("api_key")
	req.URL.RawQuery = q.Encode()
	req.Header.Del("Authorization")
	for _, name := range []string{"X-Project-ID", "X-Tavily-Orgid", "X-Tavily-Access-Mode"} {
		req.Header.Del(name)
	}
	path := strings.TrimPrefix(req.URL.Path, "/proxy/"+groupName)
	allowed := false
	if req.Method == http.MethodPost {
		switch path {
		case "/search", "/extract", "/crawl", "/map", "/research":
			allowed = true
		}
	} else if req.Method == http.MethodGet && strings.HasPrefix(path, "/research/") {
		id := strings.TrimPrefix(path, "/research/")
		allowed = id != "" && !strings.ContainsAny(id, "/\\") && id != "." && id != ".."
	}
	if !allowed {
		return nil, fmt.Errorf("unsupported Tavily method or path")
	}
	if len(body) == 0 {
		return body, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		return nil, fmt.Errorf("Tavily request body must be a JSON object")
	}
	if _, exists := payload["api_key"]; !exists {
		return body, nil
	}
	delete(payload, "api_key")
	return json.Marshal(payload)
}

func (ch *TavilyChannel) ModifyRequest(req *http.Request, key *models.APIKey, group *models.Group) {
	// Let Transport negotiate gzip and decode it instead of forwarding unsupported client encodings.
	req.Header.Del("Accept-Encoding")
	req.Header.Set("Authorization", "Bearer "+key.KeyValue)
}

func (ch *TavilyChannel) IsStreamRequest(c *gin.Context, body []byte) bool {
	return isJSONStreamRequest(c, body)
}

func (ch *TavilyChannel) ExtractModel(c *gin.Context, body []byte) string {
	if !strings.HasSuffix(c.Request.URL.Path, "/research") {
		return ""
	}
	var p struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &p) == nil && (p.Model == "mini" || p.Model == "pro") {
		return p.Model
	}
	return ""
}

func (ch *TavilyChannel) FailurePolicy(status int, transportError bool, group *models.Group) (bool, bool) {
	count := !transportError && (status == 401 || status == 432 || status == 433)
	return count && group.FailoverStatusCodeMatcher.Match(status), count
}

func (ch *TavilyChannel) ValidateKey(ctx context.Context, key *models.APIKey, group *models.Group) (bool, error) {
	target, err := ch.BuildUpstreamURL(&url.URL{Path: "/search"}, "")
	if err != nil {
		return false, fmt.Errorf("%w: %s", ErrValidationInconclusive, utils.RedactSecret(err.Error(), key.KeyValue))
	}
	body := []byte(`{"query":"hi","search_depth":"basic","max_results":1,"auto_parameters":false,"include_answer":false,"include_raw_content":false}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("%w: %s", ErrValidationInconclusive, utils.RedactSecret(err.Error(), key.KeyValue))
	}
	req.Header.Set("Content-Type", "application/json")
	utils.ApplyHeaderRules(req, group.HeaderRuleList, utils.NewHeaderVariableContext(group, key))
	ch.ModifyRequest(req, key, group)
	resp, err := ch.HTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %s", ErrValidationInconclusive, utils.RedactSecret(err.Error(), key.KeyValue))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("%w: failed to read validation response", ErrValidationInconclusive)
	}
	message := utils.RedactSecret(app_errors.ParseUpstreamError(data), key.KeyValue)
	if resp.StatusCode == 401 || resp.StatusCode == 432 || resp.StatusCode == 433 {
		return false, fmt.Errorf("[status %d] %s", resp.StatusCode, message)
	}
	return false, fmt.Errorf("%w: [status %d] %s", ErrValidationInconclusive, resp.StatusCode, message)
}
