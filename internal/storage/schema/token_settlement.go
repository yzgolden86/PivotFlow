package schema

// DefineTokenSettlementsTable stores successful token-accounting receipts.
// Receipts intentionally have no foreign key: deleting an auth token must not
// erase the globally unique event IDs needed to detect replays.
func DefineTokenSettlementsTable() *TableBuilder {
	return NewTable("token_settlements").
		Column("id VARCHAR(191) PRIMARY KEY").
		Column("token_hash VARCHAR(100) NOT NULL").
		Column("occurred_at BIGINT NOT NULL").
		Column("duration DOUBLE NOT NULL").
		Column("is_streaming TINYINT NOT NULL").
		Column("first_byte_time DOUBLE NOT NULL").
		Column("prompt_tokens BIGINT NOT NULL").
		Column("completion_tokens BIGINT NOT NULL").
		Column("cache_read_tokens BIGINT NOT NULL").
		Column("cache_creation_tokens BIGINT NOT NULL").
		Column("cost_usd DOUBLE NOT NULL").
		Column("effective_cost_usd DOUBLE NOT NULL").
		Column("cost_invalid TINYINT NOT NULL DEFAULT 0").
		Index("idx_token_settlements_token_time", "token_hash, occurred_at").
		Index("idx_token_settlements_occurred", "occurred_at")
}
