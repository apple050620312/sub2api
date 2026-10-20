package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *ServiceCenterHandler) GetResetMonitor(c *gin.Context) {
	value, err := h.service.GetResetMonitor(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

func (h *ServiceCenterHandler) UpdateResetMonitor(c *gin.Context) {
	subject, ok := onboardingSubject(c)
	if !ok {
		return
	}
	var request struct {
		Config service.ResetMonitorConfig `json:"config"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "监测配置格式无效或请求过大")
		return
	}
	value, err := h.service.UpdateResetMonitor(c.Request.Context(), request.Config, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}
