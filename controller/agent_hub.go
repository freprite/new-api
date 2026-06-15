package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type agentHubUserProvisionRequest struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	InitialQuota int    `json:"initial_quota"`
	DisplayName  string `json:"display_name"`
	Group        string `json:"group"`
}

type agentHubQuotaAdjustmentRequest struct {
	RequestId string `json:"request_id"`
	Delta     int    `json:"delta"`
	Reason    string `json:"reason"`
}

func CreateAgentHubUserProvision(c *gin.Context) {
	var req agentHubUserProvisionRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	result, err := model.EnsureAgentHubUserProvision(req.Username, req.Password, req.DisplayName, req.Group, req.InitialQuota)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, gin.H{
		"user_id": result.UserId,
		"api_key": result.ApiKey,
		"quota":   result.Quota,
	})
}

func CreateAgentHubQuotaAdjustment(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("user_id"))
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	var req agentHubQuotaAdjustmentRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	adjustment, replayed, err := model.ApplyAgentHubQuotaAdjustment(req.RequestId, userId, req.Delta, req.Reason)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, gin.H{
		"request_id":   adjustment.RequestId,
		"user_id":      adjustment.UserId,
		"delta":        adjustment.Delta,
		"quota_before": adjustment.QuotaBefore,
		"quota_after":  adjustment.QuotaAfter,
		"replayed":     replayed,
	})
}

func GetAgentHubUserQuota(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("user_id"))
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	quota, err := model.GetAgentHubUserQuota(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, gin.H{
		"user_id":    quota.UserId,
		"quota":      quota.Quota,
		"used_quota": quota.UsedQuota,
	})
}
