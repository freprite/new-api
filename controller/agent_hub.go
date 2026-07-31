package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
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
	if !replayed {
		action := "user.quota_add"
		quota := adjustment.Delta
		if adjustment.Delta < 0 {
			action = "user.quota_subtract"
			quota = -adjustment.Delta
		}
		params := map[string]interface{}{
			"quota": logger.LogQuota(quota),
		}
		targetUsername, _ := model.GetUsernameById(adjustment.UserId, false)
		callerInfo := auditOperatorInfo(c)
		adminInfo := map[string]interface{}{
			"admin_id":       adjustment.UserId,
			"admin_username": targetUsername,
			"auth_method":    auditAuthMethod(c),
		}
		if callerInfo["admin_id"] != nil {
			adminInfo["caller_admin_id"] = callerInfo["admin_id"]
		}
		if callerInfo["admin_username"] != nil {
			adminInfo["caller_admin_username"] = callerInfo["admin_username"]
		}
		if callerInfo["admin_role"] != nil {
			adminInfo["caller_admin_role"] = callerInfo["admin_role"]
		}
		model.RecordOperationAuditLog(adjustment.UserId, auditContentEN(action, params), c.ClientIP(), action, params, adminInfo, nil)
		markAuditLogged(c)
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
