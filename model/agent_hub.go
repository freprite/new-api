package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const agentHubTokenName = "agent-hub"

var (
	ErrAgentHubQuotaConflict     = errors.New("agent-hub quota adjustment request_id conflict")
	ErrAgentHubQuotaInsufficient = errors.New("agent-hub quota insufficient")
)

type AgentHubQuotaAdjustment struct {
	Id          int    `json:"id" gorm:"comment:主键 ID"`
	RequestId   string `json:"request_id" gorm:"type:varchar(64);uniqueIndex;comment:agent-hub-api 生成的全局唯一幂等请求 ID"`
	UserId      int    `json:"user_id" gorm:"index;comment:被调整配额的 New API 用户 ID"`
	Delta       int    `json:"delta" gorm:"type:int;not null;default:0;comment:配额调整值，正数为增加，负数为扣减"`
	QuotaBefore int    `json:"quota_before" gorm:"type:int;not null;default:0;comment:本次调整前的用户剩余额度"`
	QuotaAfter  int    `json:"quota_after" gorm:"type:int;not null;default:0;comment:本次调整后的用户剩余额度"`
	Reason      string `json:"reason" gorm:"type:varchar(255);default:'';comment:配额调整原因"`
	Status      string `json:"status" gorm:"type:varchar(32);index;default:'succeeded';comment:配额调整状态"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint;comment:创建时间戳，单位秒"`
	UpdatedAt   int64  `json:"updated_at" gorm:"bigint;index;comment:更新时间戳，单位秒"`
}

func (a *AgentHubQuotaAdjustment) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	a.CreatedAt = now
	a.UpdatedAt = now
	if a.Status == "" {
		a.Status = "succeeded"
	}
	return nil
}

func (a *AgentHubQuotaAdjustment) BeforeUpdate(tx *gorm.DB) error {
	a.UpdatedAt = common.GetTimestamp()
	return nil
}

type AgentHubProvisionResult struct {
	UserId int
	ApiKey string
	Quota  int
}

type AgentHubQuota struct {
	UserId    int
	Quota     int
	UsedQuota int
}

func EnsureAgentHubUserProvision(username string, password string, displayName string, group string, initialQuota int) (*AgentHubProvisionResult, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	group = strings.TrimSpace(group)
	if username == "" || password == "" {
		return nil, errors.New("username and password are required")
	}
	if len(username) > UserNameMaxLength {
		return nil, fmt.Errorf("username length must be <= %d", UserNameMaxLength)
	}
	if len(password) < 8 || len(password) > 20 {
		return nil, errors.New("password length must be between 8 and 20")
	}
	if initialQuota < 0 {
		return nil, errors.New("initial_quota must be >= 0")
	}
	if displayName == "" {
		displayName = username
	}
	if group == "" {
		group = "default"
	}

	result := &AgentHubProvisionResult{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		query := tx.Where("username = ?", username).Limit(1).Find(&user)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected == 0 {
			passwordHash, err := common.Password2Hash(password)
			if err != nil {
				return err
			}
			user = User{
				Username:    username,
				Password:    passwordHash,
				DisplayName: displayName,
				Role:        common.RoleCommonUser,
				Status:      common.UserStatusEnabled,
				Quota:       initialQuota,
				Group:       group,
				AffCode:     common.GetRandomString(4),
				Setting:     "{}",
			}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
		}

		if user.Status != common.UserStatusEnabled {
			return errors.New("agent-hub provision user is disabled")
		}

		token, err := getOrCreateAgentHubTokenTx(tx, user.Id, user.Group)
		if err != nil {
			return err
		}

		result.UserId = user.Id
		result.ApiKey = agentHubAPIKey(token.Key)
		result.Quota = user.Quota
		return nil
	})
	if err != nil {
		return nil, err
	}
	if common.RedisEnabled {
		_ = updateUserQuotaCache(result.UserId, result.Quota)
	}
	return result, nil
}

func GetAgentHubUserQuota(userId int) (*AgentHubQuota, error) {
	if userId <= 0 {
		return nil, errors.New("invalid user_id")
	}
	var user User
	if err := DB.Select("id", "quota", "used_quota").Where("id = ?", userId).First(&user).Error; err != nil {
		return nil, err
	}
	return &AgentHubQuota{
		UserId:    user.Id,
		Quota:     user.Quota,
		UsedQuota: user.UsedQuota,
	}, nil
}

func ApplyAgentHubQuotaAdjustment(requestId string, userId int, delta int, reason string) (*AgentHubQuotaAdjustment, bool, error) {
	requestId = strings.TrimSpace(requestId)
	reason = strings.TrimSpace(reason)
	if requestId == "" {
		return nil, false, errors.New("request_id is required")
	}
	if len(requestId) > 64 {
		return nil, false, errors.New("request_id length must be <= 64")
	}
	if userId <= 0 {
		return nil, false, errors.New("invalid user_id")
	}
	if delta == 0 {
		return nil, false, errors.New("delta must not be 0")
	}
	if len(reason) > 255 {
		return nil, false, errors.New("reason length must be <= 255")
	}

	var result *AgentHubQuotaAdjustment
	replayed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing AgentHubQuotaAdjustment
		query := tx.Where("request_id = ?", requestId).Limit(1).Find(&existing)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected > 0 {
			if existing.UserId != userId || existing.Delta != delta || existing.Reason != reason {
				return ErrAgentHubQuotaConflict
			}
			result = &existing
			replayed = true
			return nil
		}

		adjustment := AgentHubQuotaAdjustment{
			RequestId: requestId,
			UserId:    userId,
			Delta:     delta,
			Reason:    reason,
			Status:    "pending",
		}
		if err := tx.Create(&adjustment).Error; err != nil {
			var dup AgentHubQuotaAdjustment
			if err2 := tx.Where("request_id = ?", requestId).First(&dup).Error; err2 == nil {
				if dup.UserId != userId || dup.Delta != delta || dup.Reason != reason {
					return ErrAgentHubQuotaConflict
				}
				result = &dup
				replayed = true
				return nil
			}
			return err
		}

		update := tx.Model(&User{}).Where("id = ?", userId)
		if delta < 0 {
			update = update.Where("quota >= ?", -delta)
		}
		updateResult := update.Update("quota", gorm.Expr("quota + ?", delta))
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			var user User
			if err := tx.Select("id").Where("id = ?", userId).First(&user).Error; err != nil {
				return err
			}
			return ErrAgentHubQuotaInsufficient
		}

		var user User
		if err := tx.Select("quota").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		adjustment.QuotaAfter = user.Quota
		adjustment.QuotaBefore = user.Quota - delta
		adjustment.Status = "succeeded"
		if err := tx.Save(&adjustment).Error; err != nil {
			return err
		}
		result = &adjustment
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if common.RedisEnabled && result != nil {
		_ = updateUserQuotaCache(userId, result.QuotaAfter)
	}
	return result, replayed, nil
}

func getOrCreateAgentHubTokenTx(tx *gorm.DB, userId int, group string) (*Token, error) {
	var token Token
	now := common.GetTimestamp()
	err := tx.Where(
		"user_id = ? AND name = ? AND status = ? AND (expired_time = -1 OR expired_time > ?)",
		userId,
		agentHubTokenName,
		common.TokenStatusEnabled,
		now,
	).Order("id asc").First(&token).Error
	if err == nil {
		return &token, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	key, err := common.GenerateKey()
	if err != nil {
		return nil, err
	}
	token = Token{
		UserId:         userId,
		Name:           agentHubTokenName,
		Key:            key,
		Status:         common.TokenStatusEnabled,
		CreatedTime:    now,
		AccessedTime:   now,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          group,
	}
	if err := tx.Create(&token).Error; err != nil {
		return nil, err
	}
	return &token, nil
}

func agentHubAPIKey(key string) string {
	if strings.HasPrefix(key, "sk-") {
		return key
	}
	return "sk-" + key
}
