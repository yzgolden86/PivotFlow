package sql_test

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

// 这几张表里有一批列在 schema 上是 nullable，但读路径把它们扫进 string / int64。
// database/sql 拒绝把 NULL 赋给 string（converting NULL to string is unsupported），
// 库里一旦真出现 NULL，读路径就整条失败 —— 对 sites 而言是 /admin/sites、
// /admin/site-inventory、/admin/dashboard 三个接口一起 500。
//
// 写路径（CreateSite / CreateSiteAccount / …）永远写空串，所以正常操作碰不到；
// 但老库、备份恢复、手工 SQL 都能造出 NULL 行，属于「平时不响、响了全站挂」的那类问题。
//
// 这里刻意不列举列名：先用公开写路径建一行（保证 NOT NULL 列都有值），
// 再让 SQLite 自己把所有可空列刷成 NULL（PRAGMA table_info 的 notnull / pk 标志），
// 最后走公开读路径。将来给这几张表新增可空列时，新列会被自动刷成 NULL ——
// 读路径必须照样活得下来，否则这条用例就红。
func TestReadPathsTolerateNullInEveryNullableColumn(t *testing.T) {
	store := newTestStore(t, "nullable-columns.db")
	ss := store.(*sqlstore.SQLStore)
	ctx := context.Background()

	site, err := store.CreateSite(ctx, &model.Site{
		Name:            "null-columns",
		Platform:        model.SitePlatformNewAPIFamily,
		BaseURL:         "https://null.example.com",
		Enabled:         true,
		Timezone:        "Asia/Shanghai",
		TagsJSON:        "[]",
		LastProbeStatus: "unknown",
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID:          site.ID,
		Label:           "null-account",
		CredentialType:  model.CredentialTypeAccessToken,
		Enabled:         true,
		Status:          model.SiteAccountStatusHealthy,
		BalanceCurrency: "USD",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := store.UpsertSiteAnnouncements(ctx, []model.SiteAnnouncement{{
		SiteID:          site.ID,
		SourceKey:       "null-announcement",
		Title:           "标题",
		ContentMarkdown: "正文",
		Level:           "info",
		SourceURL:       "https://null.example.com/notice",
		FirstSeenAt:     1,
		LastSeenAt:      1,
		ContentHash:     strings.Repeat("a", 64),
		CreatedAt:       1,
		UpdatedAt:       1,
	}}); err != nil {
		t.Fatalf("upsert announcement: %v", err)
	}
	task := &model.SiteTask{
		ID:            "null-task",
		Kind:          "site_checkin",
		Status:        model.SiteTaskStatusSuccess,
		SiteID:        site.ID,
		SiteAccountID: account.ID,
		ProgressJSON:  "{}",
		CreatedAt:     1,
	}
	if err := store.CreateSiteTask(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// sites：proxy_url / external_checkin_url
	siteNulls := nullAllNullableColumns(t, ss, "sites", "id = ?", site.ID)
	for _, column := range []string{"proxy_url", "external_checkin_url"} {
		if !slices.Contains(siteNulls, column) {
			t.Fatalf("sites.%s is no longer nullable (got %v); this test no longer covers it", column, siteNulls)
		}
	}
	sites, err := store.ListSites(ctx, model.SiteListFilter{})
	if err != nil {
		t.Fatalf("ListSites with NULL proxy_url/external_checkin_url: %v", err)
	}
	got, ok := findSite(sites, site.ID)
	if !ok {
		t.Fatalf("ListSites dropped the site whose nullable columns are NULL")
	}
	if got.ProxyURL != "" || got.ExternalCheckinURL != "" {
		t.Fatalf("NULL should read back as empty: proxy_url=%q external_checkin_url=%q", got.ProxyURL, got.ExternalCheckinURL)
	}
	// GetSite 走的是同一个 siteColumns，但另一条语句，单独确认一次。
	single, err := store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatalf("GetSite with NULL proxy_url/external_checkin_url: %v", err)
	}
	if single.ProxyURL != "" || single.ExternalCheckinURL != "" {
		t.Fatalf("GetSite: NULL should read back as empty, got %q / %q", single.ProxyURL, single.ExternalCheckinURL)
	}

	// site_accounts：timezone（balance 是 DOUBLE，另有 sql.NullFloat64 兜底）
	accountNulls := nullAllNullableColumns(t, ss, "site_accounts", "id = ?", account.ID)
	if !slices.Contains(accountNulls, "timezone") {
		t.Fatalf("site_accounts.timezone is no longer nullable (got %v); this test no longer covers it", accountNulls)
	}
	accounts, err := store.ListSiteAccounts(ctx, 0, false)
	if err != nil {
		t.Fatalf("ListSiteAccounts with NULL timezone: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("ListSiteAccounts returned %d accounts, want 1", len(accounts))
	}
	if accounts[0].Timezone != "" {
		t.Fatalf("NULL timezone should read back as empty, got %q", accounts[0].Timezone)
	}
	// 空时区必须落回站点时区，否则调度器会按 UTC 判「今天」。
	if _, err := store.GetSiteAccount(ctx, account.ID); err != nil {
		t.Fatalf("GetSiteAccount with NULL timezone: %v", err)
	}

	// site_announcements：source_url
	announcementNulls := nullAllNullableColumns(t, ss, "site_announcements", "site_id = ?", site.ID)
	if !slices.Contains(announcementNulls, "source_url") {
		t.Fatalf("site_announcements.source_url is no longer nullable (got %v); this test no longer covers it", announcementNulls)
	}
	announcements, _, err := store.ListSiteAnnouncements(ctx, model.SiteAnnouncementFilter{SiteID: site.ID})
	if err != nil {
		t.Fatalf("ListSiteAnnouncements with NULL source_url: %v", err)
	}
	if len(announcements) != 1 || announcements[0].SourceURL != "" {
		t.Fatalf("NULL source_url should read back as empty, got %#v", announcements)
	}

	// site_tasks：site_id / site_account_id
	taskNulls := nullAllNullableColumns(t, ss, "site_tasks", "id = ?", task.ID)
	for _, column := range []string{"site_id", "site_account_id"} {
		if !slices.Contains(taskNulls, column) {
			t.Fatalf("site_tasks.%s is no longer nullable (got %v); this test no longer covers it", column, taskNulls)
		}
	}
	loaded, err := store.GetSiteTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetSiteTask with NULL site_id/site_account_id: %v", err)
	}
	if loaded.SiteID != 0 || loaded.SiteAccountID != 0 {
		t.Fatalf("NULL ownership should read back as 0, got site_id=%d site_account_id=%d", loaded.SiteID, loaded.SiteAccountID)
	}
}

// nullAllNullableColumns 把 where 命中的那一行的所有可空列刷成 NULL，返回被刷的列名。
// 列清单问数据库（PRAGMA table_info 的 notnull / pk 标志），不在测试里硬编码，
// 这样新增可空列会被自动带上；pk 列要排除，SQLite 里主键的 notnull 标志并不可靠。
func nullAllNullableColumns(t *testing.T, ss *sqlstore.SQLStore, table, where string, args ...any) []string {
	t.Helper()
	ctx := context.Background()

	rows, err := ss.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		t.Fatalf("read table_info(%s): %v", table, err)
	}
	var names []string
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, columnType string
			defaultValue     sql.NullString
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			_ = rows.Close()
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if notNull == 0 && pk == 0 {
			names = append(names, name)
		}
	}
	iterErr := rows.Err()
	_ = rows.Close()
	if iterErr != nil {
		t.Fatalf("iterate table_info(%s): %v", table, iterErr)
	}
	if len(names) == 0 {
		t.Fatalf("%s has no nullable column to exercise", table)
	}

	assignments := make([]string, 0, len(names))
	for _, name := range names {
		assignments = append(assignments, name+" = NULL")
	}
	if _, err := ss.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE %s", table, strings.Join(assignments, ", "), where), args...); err != nil {
		t.Fatalf("null out %s.%v: %v", table, names, err)
	}
	return names
}

func findSite(sites []*model.Site, id int64) (*model.Site, bool) {
	for _, site := range sites {
		if site.ID == id {
			return site, true
		}
	}
	return nil, false
}
