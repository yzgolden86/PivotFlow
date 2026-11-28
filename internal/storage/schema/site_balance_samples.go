package schema

// DefineSiteAccountBalanceSamplesTable keeps immutable interval observations.
// Account IDs are historical identities, so there is deliberately no cascading
// account foreign key. The legacy daily table is neither changed nor backfilled.
func DefineSiteAccountBalanceSamplesTable() *TableBuilder {
	return NewTable("site_account_balance_samples").
		Column("site_account_id INT NOT NULL").
		Column("local_day VARCHAR(10) NOT NULL").
		Column("currency VARCHAR(16) NOT NULL DEFAULT ''").
		Column("balance DOUBLE").
		Column("status VARCHAR(16) NOT NULL DEFAULT 'unknown'").
		Column("error_code VARCHAR(64) NOT NULL DEFAULT ''").
		Column("scheduled_at BIGINT NOT NULL").
		Column("collected_at BIGINT NOT NULL DEFAULT 0").
		Column("created_at BIGINT NOT NULL").
		Column("PRIMARY KEY (site_account_id, scheduled_at)").
		Index("idx_site_balance_samples_day", "local_day")
}
