// Package handler provides HTTP handlers for the application
package handler

import (
	"crypto/subtle"
	"net/http"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/config"
	"gpt-load/internal/encryption"
	"gpt-load/internal/i18n"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/response"
	"gpt-load/internal/services"
	"gpt-load/internal/types"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Server contains dependencies for HTTP handlers
type Server struct {
	ProxyManager    *outboundproxy.Manager
	DB              *gorm.DB
	config          types.ConfigManager
	SettingsManager *config.SystemSettingsManager
	GroupManager    *services.GroupManager
	GroupService    *services.GroupService
	KeyService      *services.KeyService
	LogService      *services.LogService
	EncryptionSvc   encryption.Service
}

// NewServerParams defines the dependencies for the NewServer constructor.
type NewServerParams struct {
	ProxyManager    *outboundproxy.Manager
	DB              *gorm.DB
	Config          types.ConfigManager
	SettingsManager *config.SystemSettingsManager
	GroupManager    *services.GroupManager
	GroupService    *services.GroupService
	KeyService      *services.KeyService
	LogService      *services.LogService
	EncryptionSvc   encryption.Service
}

// NewServer creates a new handler instance with explicit dependencies.
func NewServer(params NewServerParams) *Server {
	return &Server{
		ProxyManager:    params.ProxyManager,
		DB:              params.DB,
		config:          params.Config,
		SettingsManager: params.SettingsManager,
		GroupManager:    params.GroupManager,
		GroupService:    params.GroupService,
		KeyService:      params.KeyService,
		LogService:      params.LogService,
		EncryptionSvc:   params.EncryptionSvc,
	}
}

// LoginRequest represents the login request payload
type LoginRequest struct {
	AuthKey string `json:"auth_key" binding:"required"`
}

// LoginResponse represents the login response
type LoginResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// Login handles authentication verification
func (s *Server) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": i18n.Message(c, "auth.invalid_request"),
		})
		return
	}

	authConfig := s.config.GetAuthConfig()

	isValid := subtle.ConstantTimeCompare([]byte(req.AuthKey), []byte(authConfig.Key)) == 1

	if isValid {
		c.JSON(http.StatusOK, LoginResponse{
			Success: true,
			Message: i18n.Message(c, "auth.authentication_successful"),
		})
	} else {
		c.JSON(http.StatusUnauthorized, LoginResponse{
			Success: false,
			Message: i18n.Message(c, "auth.authentication_failed"),
		})
	}
}

// Health handles health check requests
func (s *Server) Health(c *gin.Context) {
	uptime := "unknown"
	if startTime, exists := c.Get("serverStartTime"); exists {
		if st, ok := startTime.(time.Time); ok {
			uptime = time.Since(st).String()
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"uptime":    uptime,
	})
}

// GetChannelTypes returns the available channel types.
func (s *Server) GetChannelTypes(c *gin.Context) {
	response.Success(c, channel.GetChannels())
}
