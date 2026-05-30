package api

import "github.com/gin-gonic/gin"

type Handlers interface {
	SetupRoutes(v1 *gin.RouterGroup)
}
