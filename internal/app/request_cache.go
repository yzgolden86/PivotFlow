package app

import (
	"bytes"
	"container/list"
	"net/http"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
)

const maxRequestCacheFlights = 4096

type requestCacheConfig struct {
	Enabled       bool `json:"enabled"`
	TTLSeconds    int  `json:"ttl_seconds"`
	MaxEntries    int  `json:"max_entries"`
	MaxBytes      int  `json:"max_bytes"`
	MaxEntryBytes int  `json:"max_entry_bytes"`
}

type requestCacheStats struct {
	Entries                int               `json:"entries"`
	Bytes                  int               `json:"bytes"`
	ExpiredEntries         int               `json:"expired_entries"`
	Captures               int               `json:"captures"`
	CaptureBytes           int               `json:"capture_bytes"`
	Lookups                uint64            `json:"lookups"`
	Hits                   uint64            `json:"hits"`
	Misses                 uint64            `json:"misses"`
	Refreshes              uint64            `json:"refreshes"`
	Bypasses               uint64            `json:"bypasses"`
	Stored                 uint64            `json:"stored"`
	Expired                uint64            `json:"expired"`
	Evicted                uint64            `json:"evicted"`
	Rejected               uint64            `json:"rejected"`
	Clears                 uint64            `json:"clears"`
	Invalidations          map[string]uint64 `json:"invalidations"`
	Reasons                map[string]uint64 `json:"reasons"`
	LastInvalidationReason string            `json:"last_invalidation_reason"`
	LastInvalidationAt     int64             `json:"last_invalidation_at"`
}

type requestCacheSnapshot struct {
	Version          int                `json:"version"`
	ObservedAt       int64              `json:"observed_at"`
	ProcessStartedAt int64              `json:"process_started_at"`
	Config           requestCacheConfig `json:"config"`
	Stats            requestCacheStats  `json:"stats"`
}

type requestCacheFlight struct {
	version uint64
	refs    int
}

type requestCacheTicket struct {
	key        [32]byte
	generation uint64
	keyVersion uint64
	flight     *requestCacheFlight
	mode       string
	reason     string
	closed     bool // protected by the owning cache mutex
}

type requestCacheEntry struct {
	key         [32]byte
	channelID   int64
	fingerprint [32]byte
	contentType string
	body        []byte
	expires     time.Time
}

// All cache bodies and diagnostic maps stay private. Lookups clone body bytes;
// no request body, credential, raw header, source request ID or key is observed.
type requestCache struct {
	mu           sync.Mutex
	config       requestCacheConfig
	epoch        uint64
	nextFlight   uint64
	entries      map[[32]byte]*list.Element
	order        *list.List
	flights      map[[32]byte]*requestCacheFlight
	payloadBytes int
	metrics      requestCacheStats
}

func newRequestCache(config requestCacheConfig) *requestCache {
	return &requestCache{config: config, entries: make(map[[32]byte]*list.Element), order: list.New(),
		flights: make(map[[32]byte]*requestCacheFlight), metrics: requestCacheStats{
			Reasons: make(map[string]uint64), Invalidations: make(map[string]uint64),
		}}
}

// openTicket happens before candidate formation. Refresh supersedes earlier
// fills for this exact key while global invalidation supersedes every key.
func (c *requestCache) openTicket(key [32]byte, mode, reason string) *requestCacheTicket {
	c.mu.Lock()
	defer c.mu.Unlock()
	flight := c.flights[key]
	if flight == nil {
		if len(c.flights) >= maxRequestCacheFlights {
			c.metrics.Bypasses++
			c.reasonLocked("capture_budget")
			return nil
		}
		c.nextFlight++
		flight = &requestCacheFlight{version: c.nextFlight}
		c.flights[key] = flight
	}
	if mode == "refresh" {
		c.nextFlight++
		flight.version = c.nextFlight
		if element := c.entries[key]; element != nil {
			c.removeLocked(element)
		}
		c.metrics.Refreshes++
		c.reasonLocked(reason)
	}
	flight.refs++
	return &requestCacheTicket{key: key, generation: c.epoch, keyVersion: flight.version, flight: flight, mode: mode, reason: reason}
}

func (c *requestCache) closeTicket(ticket *requestCacheTicket) {
	if c == nil || ticket == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ticket.closed {
		return
	}
	ticket.closed = true
	if flight := c.flights[ticket.key]; flight == ticket.flight {
		flight.refs--
		if flight.refs == 0 {
			delete(c.flights, ticket.key)
		}
	}
}

func (c *requestCache) ticketCurrentLocked(ticket *requestCacheTicket) bool {
	return ticket != nil && !ticket.closed && c.config.Enabled && ticket.generation == c.epoch &&
		c.flights[ticket.key] == ticket.flight && ticket.flight != nil && ticket.keyVersion == ticket.flight.version
}

func (c *requestCache) lookup(ticket *requestCacheTicket, candidates []*model.Config, now time.Time) (*requestCacheEntry, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ticketCurrentLocked(ticket) {
		c.metrics.Misses++
		c.reasonLocked("invalidated")
		return nil, "invalidated"
	}
	if ticket.mode == "refresh" {
		return nil, ticket.reason
	}
	c.metrics.Lookups++
	element := c.entries[ticket.key]
	reason := "empty"
	if element != nil {
		entry := element.Value.(*requestCacheEntry)
		if !now.Before(entry.expires) {
			reason = "expired"
			c.metrics.Expired++
			c.removeLocked(element)
		} else {
			var source *model.Config
			for _, cfg := range candidates {
				if cfg != nil && cfg.ID == entry.channelID {
					source = cfg
					break
				}
			}
			fingerprint, valid := requestCacheChannelFingerprint(source)
			if source == nil || !valid || fingerprint != entry.fingerprint || requestCacheChannelReason(source) != "" {
				reason = "channel_changed"
				c.removeLocked(element)
			} else {
				c.order.MoveToFront(element)
				clone := *entry
				clone.body = bytes.Clone(entry.body)
				return &clone, "reused"
			}
		}
	}
	c.metrics.Misses++
	c.reasonLocked(reason)
	return nil, reason
}

func (c *requestCache) put(ticket *requestCacheTicket, cfg *model.Config, contentType string, body []byte, now time.Time) bool {
	fingerprint, valid := requestCacheChannelFingerprint(cfg)
	if cfg == nil || !valid {
		c.reject("channel_changed")
		return false
	}
	if reason := requestCacheChannelReason(cfg); reason != "" {
		c.reject(reason)
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ticketCurrentLocked(ticket) {
		c.metrics.Rejected++
		c.reasonLocked("invalidated")
		return false
	}
	if len(body) == 0 || len(body) > c.config.MaxEntryBytes || len(body) > c.config.MaxBytes || c.config.MaxEntries <= 0 {
		c.metrics.Rejected++
		c.reasonLocked("response_too_large")
		return false
	}
	for element := c.order.Back(); element != nil; {
		previous := element.Prev()
		if !now.Before(element.Value.(*requestCacheEntry).expires) {
			c.removeLocked(element)
			c.metrics.Expired++
		}
		element = previous
	}
	if existing := c.entries[ticket.key]; existing != nil {
		c.removeLocked(existing)
	}
	for len(c.entries) >= c.config.MaxEntries || c.payloadBytes+len(body) > c.config.MaxBytes {
		c.removeLocked(c.order.Back())
		c.metrics.Evicted++
	}
	entry := &requestCacheEntry{key: ticket.key, channelID: cfg.ID, fingerprint: fingerprint, contentType: contentType,
		body: bytes.Clone(body), expires: now.Add(time.Duration(c.config.TTLSeconds) * time.Second)}
	c.entries[ticket.key] = c.order.PushFront(entry)
	c.payloadBytes += len(body)
	c.metrics.Stored++
	c.reasonLocked("stored")
	return true
}

func requestCacheChannelReason(cfg *model.Config) string {
	if cfg == nil || !cfg.Enabled {
		return "channel_changed"
	}
	if !cfg.CustomRequestRules.IsEmpty() {
		return "custom_rules"
	}
	if cfg.UsesOAuth() || isAnyrouterChannel(cfg) {
		return "protocol_transform"
	}
	return ""
}

func (c *requestCache) removeLocked(element *list.Element) {
	entry := element.Value.(*requestCacheEntry)
	delete(c.entries, entry.key)
	c.payloadBytes -= len(entry.body)
	c.order.Remove(element)
}

func (c *requestCache) invalidate(reason string, now time.Time) {
	if c == nil {
		return
	}
	switch reason {
	case "manual_clear", "routing_changed", "keys_changed", "aliases_changed", "permissions_changed":
	default:
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	c.entries = make(map[[32]byte]*list.Element)
	c.order.Init()
	c.payloadBytes = 0
	c.metrics.Invalidations[reason]++
	c.metrics.LastInvalidationReason, c.metrics.LastInvalidationAt = reason, now.UnixMilli()
	if reason == "manual_clear" {
		c.metrics.Clears++
	}
	// Existing capture leases remain accounted until their owners release them.
}

func (c *requestCache) reasonLocked(reason string) {
	if reason != "" && model.ValidRequestCacheReason(reason) {
		c.metrics.Reasons[reason]++
	}
}

func (c *requestCache) bypass(reason string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics.Bypasses++
	c.reasonLocked(reason)
}

func (c *requestCache) reject(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics.Rejected++
	c.reasonLocked(reason)
}

func (c *requestCache) deliveredHit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics.Hits++
	c.reasonLocked("reused")
}

func (c *requestCache) stats(now time.Time) requestCacheStats {
	if c == nil {
		return requestCacheStats{Reasons: map[string]uint64{}, Invalidations: map[string]uint64{}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result := c.metrics
	result.Entries, result.Bytes = len(c.entries), c.payloadBytes
	result.Invalidations = make(map[string]uint64, len(c.metrics.Invalidations))
	result.Reasons = make(map[string]uint64, len(c.metrics.Reasons))
	for reason, count := range c.metrics.Reasons {
		result.Reasons[reason] = count
	}
	for reason, count := range c.metrics.Invalidations {
		result.Invalidations[reason] = count
	}
	for _, element := range c.entries {
		if !now.Before(element.Value.(*requestCacheEntry).expires) {
			result.ExpiredEntries++
		}
	}
	return result
}

func (s *Server) requestCacheSnapshot(now time.Time) requestCacheSnapshot {
	config := defaultRequestCacheConfig()
	if s.requestCache != nil {
		config = s.requestCache.config // immutable for the process lifetime
	}
	return requestCacheSnapshot{Version: 1, ObservedAt: now.UnixMilli(), ProcessStartedAt: s.runtimeStartedAt,
		Config: config, Stats: s.requestCache.stats(now)}
}

func (s *Server) invalidateRequestCache(reason string) {
	if s != nil {
		s.requestCache.invalidate(reason, time.Now())
	}
}

// Only the explicitly chosen requests enter a cache lease. All bypasses retain
// normal proxy semantics and never allocate response capture buffers.
func (s *Server) beginRequestCache(req *http.Request, incoming incomingRequest, tokenHash string, tokenID int64, clientProtocol protocol.Protocol) (*requestCacheTicket, string) {
	if s.requestCache == nil || !s.requestCache.config.Enabled {
		s.requestCache.bypass("disabled")
		return nil, "disabled"
	}
	if tokenHash == "" || tokenID <= 0 {
		s.requestCache.bypass("no_identity")
		return nil, "no_identity"
	}
	if len(incoming.body) > s.requestCache.config.MaxEntryBytes {
		s.requestCache.bypass("unsupported_request")
		return nil, "unsupported_request"
	}
	mode, reason := classifyRequestCacheRequest(req, incoming.body, clientProtocol, incoming.isStreaming)
	if mode == "bypass" {
		s.requestCache.bypass(reason)
		return nil, reason
	}
	ticket := s.requestCache.openTicket(requestCacheKey(req, incoming.body, clientProtocol, tokenHash, tokenID), mode, reason)
	if ticket == nil {
		return nil, "capture_budget"
	}
	return ticket, reason
}
