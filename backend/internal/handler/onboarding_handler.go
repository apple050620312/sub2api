package handler

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type OnboardingHandler struct {
	service *service.OnboardingService
}

func NewOnboardingHandler(onboarding *service.OnboardingService) *OnboardingHandler {
	return &OnboardingHandler{service: onboarding}
}

func onboardingSubject(c *gin.Context) (middleware.AuthSubject, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "用户未登录")
		return middleware.AuthSubject{}, false
	}
	return subject, true
}

func (h *OnboardingHandler) GetUserOverview(c *gin.Context) {
	subject, ok := onboardingSubject(c)
	if !ok {
		return
	}
	overview, err := h.service.GetUserOverview(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, overview)
}

type onboardingRedirectRequest struct {
	RedirectURI string `json:"redirect_uri"`
}

func (h *OnboardingHandler) StartDiscord(c *gin.Context) {
	subject, ok := onboardingSubject(c)
	if !ok {
		return
	}
	var req onboardingRedirectRequest
	_ = c.ShouldBindJSON(&req)
	redirect, err := h.service.StartDiscord(c.Request.Context(), subject.UserID, req.RedirectURI)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"auth_url": redirect})
}

type onboardingDiscordCompleteRequest struct {
	Code        string `json:"code" binding:"required"`
	State       string `json:"state" binding:"required"`
	RedirectURI string `json:"redirect_uri"`
}

func (h *OnboardingHandler) CompleteDiscord(c *gin.Context) {
	subject, ok := onboardingSubject(c)
	if !ok {
		return
	}
	var req onboardingDiscordCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求格式无效："+err.Error())
		return
	}
	binding, err := h.service.CompleteDiscord(c.Request.Context(), subject.UserID, req.Code, req.State, req.RedirectURI)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, binding)
}

func (h *OnboardingHandler) StartOpenAI(c *gin.Context) {
	subject, ok := onboardingSubject(c)
	if !ok {
		return
	}
	var req onboardingRedirectRequest
	_ = c.ShouldBindJSON(&req)
	result, err := h.service.StartOpenAI(c.Request.Context(), subject.UserID, req.RedirectURI)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

type onboardingOpenAICompleteRequest struct {
	SessionID       string `json:"session_id" binding:"required"`
	Code            string `json:"code" binding:"required"`
	OAuthState      string `json:"state" binding:"required"`
	OnboardingState string `json:"onboarding_state" binding:"required"`
	RedirectURI     string `json:"redirect_uri"`
}

func (h *OnboardingHandler) CompleteOpenAI(c *gin.Context) {
	subject, ok := onboardingSubject(c)
	if !ok {
		return
	}
	var req onboardingOpenAICompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求格式无效："+err.Error())
		return
	}
	account, err := h.service.CompleteOpenAI(c.Request.Context(), subject.UserID, req.SessionID, req.Code, req.OAuthState, req.OnboardingState, req.RedirectURI)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"account_id": account.ID, "name": account.Name, "plan_type": account.GetCredential("plan_type")})
}

type onboardingConfigRequest struct {
	Enabled               *bool                                  `json:"enabled"`
	Discord               onboardingDiscordConfigRequest         `json:"discord"`
	PlanUnits             map[string]int                         `json:"plan_units"`
	SubscriptionRules     map[string]service.OnboardingSubscriptionRule `json:"subscription_rules"`
	OpenAIRedirectURI     string                                `json:"openai_redirect_uri"`
	CCSwitchDownloadURL   string                                `json:"ccswitch_download_url"`
	PublicFallbackDefault *bool                                  `json:"public_fallback_default"`
}

type onboardingDiscordConfigRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	BotToken     string `json:"bot_token"`
	GuildID      string `json:"guild_id"`
	RoleID       string `json:"role_id"`
	RedirectURI  string `json:"redirect_uri"`
}

func (h *OnboardingHandler) GetAdminOverview(c *gin.Context) {
	value, err := h.service.AdminOverview(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

func (h *OnboardingHandler) GetAdminConfig(c *gin.Context) {
	value, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

func (h *OnboardingHandler) UpdateAdminConfig(c *gin.Context) {
	var req onboardingConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求格式无效："+err.Error())
		return
	}
	next := service.OnboardingConfig{Enabled: true, PlanUnits: req.PlanUnits, SubscriptionRules: req.SubscriptionRules, OpenAIRedirectURI: req.OpenAIRedirectURI, CCSwitchDownloadURL: req.CCSwitchDownloadURL, Discord: service.OnboardingDiscordConfig{ClientID: req.Discord.ClientID, ClientSecret: req.Discord.ClientSecret, BotToken: req.Discord.BotToken, GuildID: req.Discord.GuildID, RoleID: req.Discord.RoleID, RedirectURI: req.Discord.RedirectURI}}
	if req.Enabled != nil {
		next.Enabled = *req.Enabled
	}
	if req.PublicFallbackDefault != nil {
		next.PublicFallbackDefault = *req.PublicFallbackDefault
	}
	value, err := h.service.UpdateConfig(c.Request.Context(), next)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

type onboardingBindAccountRequest struct {
	UserID        int64 `json:"user_id" binding:"required,gt=0"`
	PublicFallback bool  `json:"public_fallback"`
}

func (h *OnboardingHandler) BindAccount(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "账号 ID 无效")
		return
	}
	var req onboardingBindAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求格式无效："+err.Error())
		return
	}
	if err := h.service.BindExistingAccount(c.Request.Context(), req.UserID, accountID, req.PublicFallback); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"account_id": accountID, "user_id": req.UserID})
}

func (h *OnboardingHandler) UnbindAccount(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "账号 ID 无效")
		return
	}
	if err := h.service.UnbindAccount(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
