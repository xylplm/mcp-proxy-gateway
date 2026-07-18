package httpapi

import (
	"github.com/gin-gonic/gin"
)

func (r *Router) registerRuntimeRoutes(g *gin.RouterGroup) {
	rt := g.Group("/runtime")
	rt.GET("/summary", r.runtimeSummary)
}

func (r *Router) runtimeSummary(c *gin.Context) {
	if r.runtimeEnv == nil {
		respondServiceUnavailable(c, "运行环境服务未就绪")
		return
	}
	respondOK(c, r.runtimeEnv.Summary())
}
