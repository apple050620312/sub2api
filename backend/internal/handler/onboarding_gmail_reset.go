package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OnboardingHandler) GetGmailReset(c *gin.Context) {
	value, err := h.service.GetGmailReset(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

func (h *OnboardingHandler) UpdateGmailReset(c *gin.Context) {
	var request struct {
		Config          service.GmailResetConfig `json:"config"`
		CredentialsJSON string                   `json:"credentials_json"`
		ModelKey        string                   `json:"model_api_key"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Gmail 配置格式无效或请求过大")
		return
	}
	value, err := h.service.UpdateGmailReset(c.Request.Context(), request.Config, request.CredentialsJSON, request.ModelKey)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

func (h *OnboardingHandler) StartGmailResetOAuth(c *gin.Context) {
	subject, ok := onboardingSubject(c)
	if !ok {
		return
	}
	value, err := h.service.StartGmailResetOAuth(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"auth_url": value})
}

func (h *OnboardingHandler) CompleteGmailResetOAuth(c *gin.Context) {
	subject, ok := onboardingSubject(c)
	if !ok {
		return
	}
	var request struct {
		Code  string `json:"code" binding:"required"`
		State string `json:"state" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "缺少 Gmail 授权 code 或 state")
		return
	}
	if err := h.service.CompleteGmailResetOAuth(c.Request.Context(), subject.UserID, request.Code, request.State); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"connected": true})
}

func (h *OnboardingHandler) DisconnectGmailReset(c *gin.Context) {
	if err := h.service.DisconnectGmailReset(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"connected": false})
}
