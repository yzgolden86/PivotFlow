package app

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"

	"github.com/gin-gonic/gin"
)

func cacheTraceStatus(ticket *requestCacheTicket) string {
	if ticket.mode == "refresh" {
		return "refresh"
	}
	return "miss"
}

func (s *Server) writeRequestCacheHit(c *gin.Context, ctx context.Context, trace *requestTrace, cached *requestCacheEntry, tokenHash, originalModel string, start time.Time) int {
	if !s.authService.IsTokenActive(tokenHash) {
		trace.setCache("bypass", "invalidated")
		c.Header(requestCacheHeader, "bypass")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token expired or revoked"})
		return http.StatusUnauthorized
	}
	if !s.authService.IsChannelAllowed(tokenHash, cached.channelID) || !s.enforceTokenLimits(c, tokenHash, originalModel) {
		trace.setCache("bypass", "invalidated")
		c.Header(requestCacheHeader, "bypass")
		if !c.Writer.Written() {
			c.JSON(http.StatusForbidden, gin.H{"error": "no allowed upstream channel for this token"})
		}
		return c.Writer.Status()
	}
	if ctx.Err() != nil {
		trace.setCache("hit", "canceled")
		s.requestCache.reject("canceled")
		c.Status(StatusClientClosedRequest)
		return StatusClientClosedRequest
	}
	c.Header(requestCacheHeader, "hit")
	c.Header("Content-Type", cached.contentType)
	disableResponseWriteTimeout(c.Writer, "缓存响应")
	c.Writer.WriteHeader(http.StatusOK)
	n, err := c.Writer.Write(cached.body)
	if err == nil && n != len(cached.body) {
		err = io.ErrShortWrite
	}
	if err != nil || ctx.Err() != nil {
		reason, status := "write_failed", http.StatusBadGateway
		if ctx.Err() != nil || isClientDisconnectError(err) {
			reason, status = "canceled", StatusClientClosedRequest
		}
		trace.setCache("hit", reason)
		s.requestCache.reject(reason)
		return status
	}
	trace.setCache("hit", "reused")
	s.requestCache.deliveredHit()
	// The nil forward result is essential: a response-cache hit incurs zero
	// NEW provider tokens/cost, while counting one successful client request in
	// the existing durable, idempotent settlement path.
	s.updateTokenStatsAsync(tokenHash, 1, nil, true, time.Since(start).Seconds(), false, nil, originalModel)
	return http.StatusOK
}

func (s *Server) finishRequestCacheFill(ctx context.Context, trace *requestTrace, ticket *requestCacheTicket, capture *requestCacheCapture, result *proxyResult, candidates []*model.Config, client protocol.Protocol, tokenHash, lookupReason string) {
	status, header, body, reason := capture.snapshot()
	if ctx.Err() != nil {
		reason = "canceled"
	} else if !s.authService.IsTokenActive(tokenHash) {
		reason = "invalidated"
	} else if reason == "" {
		reason = requestCacheResponseReason(client, status, header, body)
	}
	var source *model.Config
	if result != nil && result.channelID != nil {
		for _, cfg := range candidates {
			if cfg.ID == *result.channelID {
				source = cfg
				break
			}
		}
	}
	if reason == "" {
		reason = requestCacheChannelReason(source)
	}
	if reason != "" {
		trace.setCache(cacheTraceStatus(ticket), reason)
		s.requestCache.reject(reason)
		return
	}
	if !s.requestCache.put(ticket, source, header.Get("Content-Type"), body, time.Now()) {
		trace.setCache(cacheTraceStatus(ticket), "invalidated")
		return
	}
	trace.setCache(cacheTraceStatus(ticket), lookupReason)
}
