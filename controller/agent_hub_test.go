package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type agentHubProvisionResponse struct {
	UserId int    `json:"user_id"`
	ApiKey string `json:"api_key"`
	Quota  int    `json:"quota"`
}

type agentHubQuotaAdjustmentResponse struct {
	RequestId   string `json:"request_id"`
	UserId      int    `json:"user_id"`
	Delta       int    `json:"delta"`
	QuotaBefore int    `json:"quota_before"`
	QuotaAfter  int    `json:"quota_after"`
	Replayed    bool   `json:"replayed"`
}

type agentHubQuotaResponse struct {
	UserId    int `json:"user_id"`
	Quota     int `json:"quota"`
	UsedQuota int `json:"used_quota"`
}

func setupAgentHubControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	model.DB = db
	model.LOG_DB = db

	if err := db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.AgentHubQuotaAdjustment{},
		&model.Log{},
	); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func seedAgentHubUser(t *testing.T, db *gorm.DB, username string, quota int, usedQuota int) *model.User {
	t.Helper()

	user := &model.User{
		Username:    username,
		Password:    "hashed-password",
		DisplayName: username,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       quota,
		UsedQuota:   usedQuota,
		Group:       "default",
		AffCode:     common.GetRandomString(4),
		Setting:     "{}",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	return user
}

func TestAgentHubUserProvisionCreatesAndReusesToken(t *testing.T) {
	db := setupAgentHubControllerTestDB(t)
	body := map[string]any{
		"username":      "ah_user_1",
		"password":      "pass12345",
		"initial_quota": 123,
		"display_name":  "Agent Hub User",
		"group":         "default",
	}

	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/agent-hub/user-provisions", body, 1)
	CreateAgentHubUserProvision(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected provision to succeed, got message: %s", response.Message)
	}
	var provision agentHubProvisionResponse
	if err := common.Unmarshal(response.Data, &provision); err != nil {
		t.Fatalf("failed to decode provision response: %v", err)
	}
	if provision.UserId == 0 {
		t.Fatalf("expected provision user_id")
	}
	if provision.Quota != 123 {
		t.Fatalf("expected quota 123, got %d", provision.Quota)
	}
	if !strings.HasPrefix(provision.ApiKey, "sk-") {
		t.Fatalf("expected api_key to include sk- prefix, got %q", provision.ApiKey)
	}

	var tokenCount int64
	if err := db.Model(&model.Token{}).Where("user_id = ?", provision.UserId).Count(&tokenCount).Error; err != nil {
		t.Fatalf("failed to count tokens: %v", err)
	}
	if tokenCount != 1 {
		t.Fatalf("expected one token, got %d", tokenCount)
	}

	ctx, recorder = newAuthenticatedContext(t, http.MethodPost, "/api/agent-hub/user-provisions", body, 1)
	CreateAgentHubUserProvision(ctx)
	response = decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected repeated provision to succeed, got message: %s", response.Message)
	}
	var repeated agentHubProvisionResponse
	if err := common.Unmarshal(response.Data, &repeated); err != nil {
		t.Fatalf("failed to decode repeated provision response: %v", err)
	}
	if repeated.UserId != provision.UserId || repeated.ApiKey != provision.ApiKey {
		t.Fatalf("expected repeated provision to reuse user/key, first=%+v repeated=%+v", provision, repeated)
	}
	if err := db.Model(&model.Token{}).Where("user_id = ?", provision.UserId).Count(&tokenCount).Error; err != nil {
		t.Fatalf("failed to count repeated tokens: %v", err)
	}
	if tokenCount != 1 {
		t.Fatalf("expected repeated provision to keep one token, got %d", tokenCount)
	}
}

func TestAgentHubUserProvisionReusesExistingUserAndCreatesToken(t *testing.T) {
	db := setupAgentHubControllerTestDB(t)
	user := seedAgentHubUser(t, db, "ah_exists", 100, 0)

	body := map[string]any{
		"username":      "ah_exists",
		"password":      "pass12345",
		"initial_quota": 123,
	}
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/agent-hub/user-provisions", body, 1)
	CreateAgentHubUserProvision(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected provision to reuse existing user, got message: %s", response.Message)
	}
	var provision agentHubProvisionResponse
	if err := common.Unmarshal(response.Data, &provision); err != nil {
		t.Fatalf("failed to decode provision response: %v", err)
	}
	if provision.UserId != user.Id || provision.Quota != 100 {
		t.Fatalf("expected existing user/quota to be reused, got %+v", provision)
	}
	if !strings.HasPrefix(provision.ApiKey, "sk-") {
		t.Fatalf("expected api_key to include sk- prefix, got %q", provision.ApiKey)
	}
	var tokenCount int64
	if err := db.Model(&model.Token{}).Where("user_id = ? AND name = ?", user.Id, "agent-hub").Count(&tokenCount).Error; err != nil {
		t.Fatalf("failed to count agent-hub tokens: %v", err)
	}
	if tokenCount != 1 {
		t.Fatalf("expected one agent-hub token, got %d", tokenCount)
	}
}

func TestAgentHubQuotaAdjustmentIdempotencyAndQuery(t *testing.T) {
	db := setupAgentHubControllerTestDB(t)
	user := seedAgentHubUser(t, db, "ah_quota", 100, 7)

	body := map[string]any{
		"request_id": "media-1-pre",
		"delta":      -30,
		"reason":     "pre_deduct",
	}
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/agent-hub/users/"+strconv.Itoa(user.Id)+"/quota-adjustments", body, 1)
	ctx.Set("username", "root")
	ctx.Set("role", common.RoleRootUser)
	ctx.Set("use_access_token", true)
	ctx.Params = gin.Params{{Key: "user_id", Value: strconv.Itoa(user.Id)}}
	CreateAgentHubQuotaAdjustment(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected quota adjustment to succeed, got message: %s", response.Message)
	}
	var adjustment agentHubQuotaAdjustmentResponse
	if err := common.Unmarshal(response.Data, &adjustment); err != nil {
		t.Fatalf("failed to decode quota adjustment: %v", err)
	}
	if adjustment.QuotaBefore != 100 || adjustment.QuotaAfter != 70 || adjustment.Replayed {
		t.Fatalf("unexpected first adjustment response: %+v", adjustment)
	}
	assertAgentHubQuotaAuditLog(t, db, user, "user.quota_subtract", logger.LogQuota(30), 1)

	ctx, recorder = newAuthenticatedContext(t, http.MethodPost, "/api/agent-hub/users/"+strconv.Itoa(user.Id)+"/quota-adjustments", body, 1)
	ctx.Set("username", "root")
	ctx.Set("role", common.RoleRootUser)
	ctx.Set("use_access_token", true)
	ctx.Params = gin.Params{{Key: "user_id", Value: strconv.Itoa(user.Id)}}
	CreateAgentHubQuotaAdjustment(ctx)
	response = decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected repeated quota adjustment to succeed, got message: %s", response.Message)
	}
	var repeated agentHubQuotaAdjustmentResponse
	if err := common.Unmarshal(response.Data, &repeated); err != nil {
		t.Fatalf("failed to decode repeated quota adjustment: %v", err)
	}
	if !repeated.Replayed || repeated.QuotaAfter != 70 {
		t.Fatalf("expected repeated adjustment replay with quota 70, got %+v", repeated)
	}
	assertAgentHubQuotaAuditLog(t, db, user, "user.quota_subtract", logger.LogQuota(30), 1)

	conflictBody := map[string]any{
		"request_id": "media-1-pre",
		"delta":      -20,
		"reason":     "pre_deduct",
	}
	ctx, recorder = newAuthenticatedContext(t, http.MethodPost, "/api/agent-hub/users/"+strconv.Itoa(user.Id)+"/quota-adjustments", conflictBody, 1)
	ctx.Set("username", "root")
	ctx.Set("role", common.RoleRootUser)
	ctx.Set("use_access_token", true)
	ctx.Params = gin.Params{{Key: "user_id", Value: strconv.Itoa(user.Id)}}
	CreateAgentHubQuotaAdjustment(ctx)
	response = decodeAPIResponse(t, recorder)
	if response.Success {
		t.Fatalf("expected conflicting quota adjustment to fail")
	}
	assertAgentHubQuotaAuditLog(t, db, user, "user.quota_subtract", logger.LogQuota(30), 1)

	insufficientBody := map[string]any{
		"request_id": "media-2-pre",
		"delta":      -1000,
	}
	ctx, recorder = newAuthenticatedContext(t, http.MethodPost, "/api/agent-hub/users/"+strconv.Itoa(user.Id)+"/quota-adjustments", insufficientBody, 1)
	ctx.Set("username", "root")
	ctx.Set("role", common.RoleRootUser)
	ctx.Set("use_access_token", true)
	ctx.Params = gin.Params{{Key: "user_id", Value: strconv.Itoa(user.Id)}}
	CreateAgentHubQuotaAdjustment(ctx)
	response = decodeAPIResponse(t, recorder)
	if response.Success {
		t.Fatalf("expected insufficient quota adjustment to fail")
	}
	assertAgentHubQuotaAuditLog(t, db, user, "user.quota_subtract", logger.LogQuota(30), 1)

	addBody := map[string]any{
		"request_id": "media-3-refund",
		"delta":      15,
		"reason":     "refund",
	}
	ctx, recorder = newAuthenticatedContext(t, http.MethodPost, "/api/agent-hub/users/"+strconv.Itoa(user.Id)+"/quota-adjustments", addBody, 1)
	ctx.Set("username", "root")
	ctx.Set("role", common.RoleRootUser)
	ctx.Set("use_access_token", true)
	ctx.Params = gin.Params{{Key: "user_id", Value: strconv.Itoa(user.Id)}}
	CreateAgentHubQuotaAdjustment(ctx)
	response = decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected positive quota adjustment to succeed, got message: %s", response.Message)
	}
	var added agentHubQuotaAdjustmentResponse
	if err := common.Unmarshal(response.Data, &added); err != nil {
		t.Fatalf("failed to decode positive quota adjustment: %v", err)
	}
	if added.QuotaBefore != 70 || added.QuotaAfter != 85 || added.Replayed {
		t.Fatalf("unexpected positive adjustment response: %+v", added)
	}
	assertAgentHubQuotaAuditLog(t, db, user, "user.quota_add", logger.LogQuota(15), 2)

	ctx, recorder = newAuthenticatedContext(t, http.MethodGet, "/api/agent-hub/users/"+strconv.Itoa(user.Id)+"/quota", nil, 1)
	ctx.Params = gin.Params{{Key: "user_id", Value: strconv.Itoa(user.Id)}}
	GetAgentHubUserQuota(ctx)
	response = decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected quota query to succeed, got message: %s", response.Message)
	}
	var quota agentHubQuotaResponse
	if err := common.Unmarshal(response.Data, &quota); err != nil {
		t.Fatalf("failed to decode quota response: %v", err)
	}
	if quota.UserId != user.Id || quota.Quota != 85 || quota.UsedQuota != 7 {
		t.Fatalf("unexpected quota response: %+v", quota)
	}
}

func assertAgentHubQuotaAuditLog(t *testing.T, db *gorm.DB, user *model.User, action string, quota string, count int64) {
	t.Helper()

	var logs []model.Log
	if err := db.Where("type = ?", model.LogTypeManage).Order("id asc").Find(&logs).Error; err != nil {
		t.Fatalf("failed to query audit logs: %v", err)
	}
	if int64(len(logs)) != count {
		t.Fatalf("expected %d manage audit logs, got %d", count, len(logs))
	}
	if len(logs) == 0 {
		return
	}

	log := logs[len(logs)-1]
	if log.UserId != user.Id || log.Username != user.Username {
		t.Fatalf("expected audit log to belong to target user %d/%s, got user_id=%d username=%s", user.Id, user.Username, log.UserId, log.Username)
	}
	if strings.Contains(log.Content, "/api/agent-hub") {
		t.Fatalf("expected audit log content to describe quota adjustment, got route content: %s", log.Content)
	}
	expectedContent := map[string]string{
		"user.quota_add":      "Increased user quota by " + quota,
		"user.quota_subtract": "Decreased user quota by " + quota,
	}[action]
	if log.Content != expectedContent {
		t.Fatalf("expected audit log content %q, got %q", expectedContent, log.Content)
	}

	other, err := common.StrToMap(log.Other)
	if err != nil {
		t.Fatalf("failed to decode log other: %v", err)
	}
	op, ok := other["op"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected log other.op, got %+v", other)
	}
	if op["action"] != action {
		t.Fatalf("expected op action %s, got %+v", action, op["action"])
	}
	params, ok := op["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected op params, got %+v", op)
	}
	if params["quota"] != quota {
		t.Fatalf("expected quota param %q, got %+v", quota, params["quota"])
	}
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected admin_info, got %+v", other)
	}
	if adminInfo["auth_method"] != "access_token" {
		t.Fatalf("expected access_token auth method, got %+v", adminInfo["auth_method"])
	}
}
