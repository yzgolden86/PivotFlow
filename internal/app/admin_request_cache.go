package app

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// AdminGetRequestCache reads only current-process memory. Expiry cleanup is
// intentionally reserved for real cache traffic, never for an observation.
func (s *Server) AdminGetRequestCache(c *gin.Context) {
	RespondJSON(c, http.StatusOK, s.requestCacheSnapshot(time.Now()))
}

func (s *Server) AdminClearRequestCache(c *gin.Context) {
	s.invalidateRequestCache("manual_clear")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": s.requestCacheSnapshot(time.Now()),
		"message": "请求缓存已清空；在途旧结果不会重新填入，进程累计计数保留"})
}
