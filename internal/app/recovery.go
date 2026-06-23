package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

func safeRecoveryMiddleware(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				requestID := c.GetString(requestIDKey)
				method, path := requestInfo(c)
				logger.Error("HTTP request panic recovered",
					"requestId", requestID,
					"method", method,
					"path", path,
					"panic", fmt.Sprint(recovered),
					"stack", string(debug.Stack()),
				)

				if c.Writer.Written() {
					c.Abort()
					return
				}
				respondRecoveredPanic(c, path)
			}
		}()
		c.Next()
	}
}

func requestInfo(c *gin.Context) (method, path string) {
	if c == nil || c.Request == nil {
		return "", ""
	}
	method = c.Request.Method
	if c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	return method, path
}

func respondRecoveredPanic(c *gin.Context, path string) {
	message := "\u670d\u52a1\u5668\u5185\u90e8\u9519\u8bef"
	if strings.HasPrefix(path, "/api/") {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"code":    50000,
			"message": message,
			"data":    nil,
		})
		return
	}
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
		"code":    string(domain.CodeInternal),
		"message": message,
	})
}
