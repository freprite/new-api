package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func setAgentHubRouter(apiRouter *gin.RouterGroup) {
	agentHubRoute := apiRouter.Group("/agent-hub")
	agentHubRoute.Use(middleware.RootAuth())
	{
		agentHubRoute.POST("/user-provisions", controller.CreateAgentHubUserProvision)
		agentHubRoute.POST("/users/:user_id/quota-adjustments", controller.CreateAgentHubQuotaAdjustment)
		agentHubRoute.GET("/users/:user_id/quota", controller.GetAgentHubUserQuota)
	}
}
