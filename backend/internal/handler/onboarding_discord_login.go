package handler

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauth"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const discordLoginStateCookie = "discord_login_state"
const discordLoginStatePrefix = "discord-login."
const discordLoginCookiePath = "/api/v1/auth/oauth/discord"

func (h *OnboardingHandler) DiscordOnlyAuthGuard(c *gin.Context) {
	if h == nil || h.service == nil {
		c.Next()
		return
	}
	switch c.Request.URL.Path {
	case "/api/v1/auth/oauth/discord/status", "/api/v1/auth/oauth/discord/start", "/api/v1/auth/oauth/discord/complete", "/api/v1/auth/refresh", "/api/v1/auth/logout":
		c.Next()
	default:
		response.ErrorFrom(c, infraerrors.Forbidden("DISCORD_LOGIN_REQUIRED", "本站仅支持 Discord 登录或注册"))
		c.Abort()
	}
}

func (h *OnboardingHandler) DiscordLoginStatus(c *gin.Context) {
	ready, err := h.service.DiscordLoginReady(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"enabled": true, "ready": ready})
}

func (h *OnboardingHandler) DiscordLoginStart(c *gin.Context) {
	state, err := oauth.GenerateState()
	if err != nil {
		response.ErrorFrom(c, service.ErrServiceUnavailable)
		return
	}
	state = discordLoginStatePrefix + state
	authorizeURL, err := h.service.StartDiscordLoginAuthorization(c.Request.Context(), state)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(discordLoginStateCookie, state, 600, discordLoginCookiePath, "", isRequestHTTPS(c), true)
	response.Success(c, gin.H{"authorize_url": authorizeURL})
}

func (h *OnboardingHandler) DiscordLoginComplete(c *gin.Context) {
	var req struct {
		Code  string `json:"code" binding:"required"`
		State string `json:"state" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Discord 回调参数无效，请重新登录")
		return
	}
	expected, err := c.Cookie(discordLoginStateCookie)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(discordLoginStateCookie, "", -1, discordLoginCookiePath, "", isRequestHTTPS(c), true)
	if err != nil || !strings.HasPrefix(req.State, discordLoginStatePrefix) || subtle.ConstantTimeCompare([]byte(expected), []byte(req.State)) != 1 {
		response.ErrorFrom(c, infraerrors.BadRequest("DISCORD_LOGIN_STATE_INVALID", "Discord 登录状态无效或已过期，请重新登录"))
		return
	}
	profile, err := h.service.CompleteDiscordLoginAuthorization(c.Request.Context(), req.Code)
	if err != nil {
		respondDiscordLoginError(c, err)
		return
	}
	if h.auth == nil {
		respondDiscordLoginError(c, errors.New("auth service is unavailable"))
		return
	}
	_, user, err := h.auth.LoginOrRegisterDiscordEmail(c.Request.Context(), profile.Email, profile.Username)
	if err != nil {
		respondDiscordLoginError(c, err)
		return
	}
	if h.settings != nil && h.settings.IsBackendModeEnabled(c.Request.Context()) && user.Role != service.RoleAdmin {
		response.Forbidden(c, "后台模式仅允许管理员登录")
		return
	}
	if err := h.service.BindDiscordLogin(c.Request.Context(), user.ID, profile); err != nil {
		respondDiscordLoginError(c, err)
		return
	}
	respondWithTokenPair(c, h.auth, user)
}

func respondDiscordLoginError(c *gin.Context, err error) {
	var applicationError *infraerrors.ApplicationError
	if errors.As(err, &applicationError) {
		response.ErrorFrom(c, err)
		return
	}
	response.ErrorFrom(c, infraerrors.ServiceUnavailable("DISCORD_LOGIN_UNAVAILABLE", "Discord 登录服务暂时不可用，请稍后重试"))
}
