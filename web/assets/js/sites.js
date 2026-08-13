(function () {
  'use strict';

  const state = {
    sites: [],
    accountsBySite: new Map(),
    announcements: [],
    models: [],
    siteSearch: '',
    modelSearch: '',
    modelAccountID: '',
    toastTimer: null,
  };

  const terminalTaskStatuses = new Set(['success', 'partial', 'failed', 'cancelled']);

  function escapeHTML(value) {
    return String(value ?? '')
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#039;');
  }

  function formatTime(timestamp) {
    if (!timestamp) return '—';
    return new Intl.DateTimeFormat('zh-CN', {
      month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
    }).format(new Date(timestamp));
  }

  function formatBalance(account) {
    if (account.balance === null || account.balance === undefined) return '—';
    const currency = account.balance_currency || 'CNY';
    try {
      return new Intl.NumberFormat('zh-CN', {
        style: 'currency', currency, maximumFractionDigits: 2,
      }).format(account.balance);
    } catch (_) {
      return `${Number(account.balance).toFixed(2)} ${currency}`;
    }
  }

  function statusLabel(status) {
    const labels = {
      healthy: '正常', degraded: '降级', expired: '已过期', disabled: '已禁用',
      error: '异常', unknown: '未知', success: '成功', failed: '失败',
      already_checked: '已签到', browser_required: '需浏览器', unsupported: '不支持',
      running: '运行中', queued: '排队中', partial: '部分成功', cancelled: '已取消',
    };
    return labels[status] || status || '未知';
  }

  function badgeClass(status) {
    if (['healthy', 'success', 'already_checked'].includes(status)) return 'site-badge-success';
    if (['degraded', 'partial', 'browser_required', 'running', 'queued'].includes(status)) return 'site-badge-warning';
    if (['expired', 'error', 'failed'].includes(status)) return 'site-badge-error';
    if (status === 'disabled' || status === 'cancelled') return 'site-badge-muted';
    return 'site-badge-info';
  }

  function showToast(message, type = 'info') {
    const toast = document.getElementById('sites-toast');
    if (!toast) return;
    window.clearTimeout(state.toastTimer);
    toast.textContent = message;
    toast.className = `sites-toast ${type}`;
    toast.hidden = false;
    state.toastTimer = window.setTimeout(() => { toast.hidden = true; }, 3200);
  }

  function apiError(error) {
    const code = String(error?.message || error || '请求失败');
    const labels = {
      credential_locked: '凭证主密钥未配置或不可用',
      browser_required: '站点需要浏览器验证',
      provider_timeout: '站点请求超时',
      provider_rate_limited: '站点触发限流，请稍后重试',
      api_key_required: '投影需要 API Key',
      models_required: '请先刷新该账号的模型列表',
      unsupported: '当前站点或凭证不支持此操作',
      conflict: '已有任务正在运行或投影存在冲突',
      expired: '凭证已过期',
    };
    return labels[code] || code;
  }

  function openModal(id) {
    const modal = document.getElementById(id);
    if (!modal) return;
    modal.setAttribute('aria-hidden', 'false');
    document.body.style.overflow = 'hidden';
  }

  function closeModal(id) {
    const modal = document.getElementById(id);
    if (!modal) return;
    modal.setAttribute('aria-hidden', 'true');
    if (!document.querySelector('.sites-modal[aria-hidden="false"]')) {
      document.body.style.overflow = '';
    }
  }

  async function request(url, options = {}) {
    return window.fetchDataWithAuth(url, options);
  }

  async function requestPayload(url, options = {}) {
    const payload = await window.fetchAPIWithAuth(url, options);
    if (!payload.success) throw new Error(payload.error || '请求失败');
    return payload;
  }

  async function loadSites() {
    const container = document.getElementById('site-cards');
    container.innerHTML = '<div class="sites-loading">加载中…</div>';
    try {
      state.sites = await request('/admin/sites');
      const accountPairs = await Promise.all(state.sites.map(async (site) => {
        try {
          return [site.id, await request(`/admin/sites/${site.id}/accounts`)];
        } catch (_) {
          return [site.id, []];
        }
      }));
      state.accountsBySite = new Map(accountPairs);
      renderSites();
      populateAccountFilter();
    } catch (error) {
      container.innerHTML = `<div class="sites-empty-state">加载失败：${escapeHTML(apiError(error))}</div>`;
    }
  }

  function renderSites() {
    const container = document.getElementById('site-cards');
    const empty = document.getElementById('site-empty');
    const query = state.siteSearch.trim().toLowerCase();
    const sites = state.sites.filter((site) => {
      if (!query) return true;
      return [site.name, site.base_url, site.platform].some((value) => String(value || '').toLowerCase().includes(query));
    });

    empty.hidden = state.sites.length > 0;
    container.hidden = state.sites.length === 0;
    if (!sites.length && state.sites.length) {
      container.innerHTML = '<div class="sites-empty-state">没有匹配的站点</div>';
      return;
    }

    container.innerHTML = sites.map((site) => {
      const accounts = state.accountsBySite.get(site.id) || [];
      const totalBalance = accounts.reduce((sum, account) => sum + (Number(account.balance) || 0), 0);
      const healthy = accounts.filter((account) => account.status === 'healthy').length;
      const probeStatus = site.last_probe_status || 'unknown';
      return `
        <article class="site-card" data-site-id="${site.id}">
          <header class="site-card-header">
            <div>
              <h2 class="site-card-name">${escapeHTML(site.name)}</h2>
              <div class="site-card-url">${escapeHTML(site.base_url)}</div>
              <div class="site-card-badges">
                <span class="site-badge ${site.enabled ? 'site-badge-success' : 'site-badge-muted'}">${site.enabled ? '已启用' : '已禁用'}</span>
                <span class="site-badge ${badgeClass(probeStatus)}">探测：${escapeHTML(statusLabel(probeStatus))}</span>
                <span class="site-badge site-badge-info">${escapeHTML(site.platform || 'unknown')}</span>
              </div>
            </div>
            <div class="site-card-actions">
              <button type="button" class="btn-icon" data-action="probe-site" data-site-id="${site.id}" title="探测站点" aria-label="探测站点">↻</button>
              <button type="button" class="btn-icon" data-action="edit-site" data-site-id="${site.id}" title="编辑站点" aria-label="编辑站点">✎</button>
              <button type="button" class="btn-icon" data-action="delete-site" data-site-id="${site.id}" title="删除站点" aria-label="删除站点">×</button>
            </div>
          </header>
          <div class="site-card-body">
            <div class="site-card-summary">
              <div class="site-summary-item"><span class="site-summary-value">${accounts.length}</span><span class="site-summary-label">账号</span></div>
              <div class="site-summary-item"><span class="site-summary-value">${healthy}/${accounts.length}</span><span class="site-summary-label">正常</span></div>
              <div class="site-summary-item"><span class="site-summary-value">${totalBalance ? totalBalance.toFixed(2) : '—'}</span><span class="site-summary-label">已知余额</span></div>
            </div>
            <div class="account-list">
              ${accounts.length ? accounts.map((account) => renderAccount(account)).join('') : '<div class="account-list-empty">暂无账号</div>'}
            </div>
            <button type="button" class="account-add-button" data-action="add-account" data-site-id="${site.id}">+ 添加账号</button>
          </div>
        </article>`;
    }).join('');
  }

  function renderAccount(account) {
    const taskBusy = account._busy === true;
    const balance = formatBalance(account);
    return `
      <div class="account-row" data-account-id="${account.id}">
        <div class="account-row-main">
          <div class="account-row-info">
            <span class="account-row-label">${escapeHTML(account.label)}</span>
            <span class="site-badge ${badgeClass(account.status)}">${escapeHTML(statusLabel(account.status))}</span>
          </div>
          <div class="account-row-meta">
            <span>${escapeHTML(balance)}</span>
            <span>签到：${escapeHTML(statusLabel(account.last_checkin_status))}</span>
            <span>${escapeHTML(account.credential_type)}</span>
          </div>
        </div>
        <div class="account-row-actions">
          <button type="button" class="account-action-btn primary" data-action="checkin-account" data-account-id="${account.id}" ${taskBusy ? 'disabled' : ''}>签到</button>
          <button type="button" class="account-action-btn" data-action="refresh-account" data-account-id="${account.id}" ${taskBusy ? 'disabled' : ''}>余额</button>
          <button type="button" class="account-action-btn" data-action="refresh-models" data-account-id="${account.id}" ${taskBusy ? 'disabled' : ''}>模型</button>
          <button type="button" class="account-action-btn" data-action="project-account" data-account-id="${account.id}">投影</button>
          <button type="button" class="account-icon-btn" data-action="show-checkins" data-account-id="${account.id}" title="签到记录" aria-label="签到记录">≡</button>
          <button type="button" class="account-icon-btn" data-action="edit-account" data-account-id="${account.id}" title="编辑账号" aria-label="编辑账号">✎</button>
          <button type="button" class="account-icon-btn danger" data-action="delete-account" data-account-id="${account.id}" title="删除账号" aria-label="删除账号">×</button>
        </div>
      </div>`;
  }

  function allAccounts() {
    return [...state.accountsBySite.values()].flat();
  }

  function findSite(siteID) {
    return state.sites.find((site) => site.id === Number(siteID));
  }

  function findAccount(accountID) {
    return allAccounts().find((account) => account.id === Number(accountID));
  }

  function setAccountBusy(accountID, busy) {
    const account = findAccount(accountID);
    if (account) account._busy = busy;
    renderSites();
  }

  function openSiteForm(site = null) {
    document.getElementById('site-modal-title').textContent = site ? '编辑站点' : '添加站点';
    document.getElementById('site-form-id').value = site?.id || '';
    document.getElementById('site-form-name').value = site?.name || '';
    document.getElementById('site-form-url').value = site?.base_url || '';
    document.getElementById('site-form-platform').value = site?.platform || 'new-api-family';
    document.getElementById('site-form-timezone').value = site?.timezone || 'Asia/Shanghai';
    document.getElementById('site-form-proxy').value = site?.proxy_url || '';
    document.getElementById('site-form-enabled').checked = site?.enabled ?? true;
    openModal('site-modal');
    document.getElementById('site-form-name').focus();
  }

  async function submitSiteForm(event) {
    event.preventDefault();
    const id = document.getElementById('site-form-id').value;
    const body = {
      name: document.getElementById('site-form-name').value.trim(),
      base_url: document.getElementById('site-form-url').value.trim(),
      platform: document.getElementById('site-form-platform').value,
      timezone: document.getElementById('site-form-timezone').value.trim() || 'Asia/Shanghai',
      proxy_url: document.getElementById('site-form-proxy').value.trim(),
    };
    const enabled = document.getElementById('site-form-enabled').checked;
    if (id) body.enabled = enabled;
    try {
      const saved = await request(id ? `/admin/sites/${id}` : '/admin/sites', {
        method: id ? 'PATCH' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (!id && !enabled) {
        await request(`/admin/sites/${saved.id}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled: false }),
        });
      }
      closeModal('site-modal');
      showToast(id ? '站点已更新' : '站点已添加', 'success');
      await loadSites();
    } catch (error) {
      showToast(apiError(error), 'error');
    }
  }

  function openAccountForm(siteID, account = null) {
    const editing = Boolean(account);
    document.getElementById('account-modal-title').textContent = editing ? '编辑账号' : '添加账号';
    document.getElementById('account-form-id').value = account?.id || '';
    document.getElementById('account-form-site-id').value = siteID;
    document.getElementById('account-form-label').value = account?.label || '';
    document.getElementById('account-form-credential-type').value = account?.credential_type || 'access_token';
    document.getElementById('account-form-credential-type').disabled = editing;
    document.getElementById('account-form-credential').value = '';
    document.getElementById('account-credential-input-row').hidden = editing;
    document.getElementById('account-credential-hint').hidden = true;
    document.getElementById('account-form-timezone').value = account?.timezone || '';
    document.getElementById('account-form-enabled').checked = account?.enabled ?? true;
    document.getElementById('account-form-auto-checkin').checked = account?.auto_checkin ?? true;
    document.getElementById('account-form-auto-refresh').checked = account?.auto_refresh ?? true;
    updateCredentialLabel();
    openModal('account-modal');
    document.getElementById('account-form-label').focus();
  }

  function updateCredentialLabel() {
    const type = document.getElementById('account-form-credential-type').value;
    document.getElementById('account-credential-label').textContent = type === 'api_key' ? 'API Key' : 'Access Token';
  }

  async function submitAccountForm(event) {
    event.preventDefault();
    const id = document.getElementById('account-form-id').value;
    const siteID = document.getElementById('account-form-site-id').value;
    const body = {
      label: document.getElementById('account-form-label').value.trim(),
      enabled: document.getElementById('account-form-enabled').checked,
      auto_checkin: document.getElementById('account-form-auto-checkin').checked,
      auto_refresh: document.getElementById('account-form-auto-refresh').checked,
      timezone: document.getElementById('account-form-timezone').value.trim(),
    };
    if (!id) {
      const type = document.getElementById('account-form-credential-type').value;
      const credentialValue = document.getElementById('account-form-credential').value.trim();
      if (!credentialValue) {
        showToast('请输入凭证', 'warning');
        return;
      }
      body.credential_type = type;
      body.credential = type === 'api_key' ? { api_key: credentialValue } : { access_token: credentialValue };
    }
    try {
      await request(id ? `/admin/site-accounts/${id}` : `/admin/sites/${siteID}/accounts`, {
        method: id ? 'PATCH' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      closeModal('account-modal');
      showToast(id ? '账号已更新' : '账号已添加', 'success');
      await loadSites();
    } catch (error) {
      showToast(apiError(error), 'error');
    }
  }

  async function probeSite(siteID) {
    try {
      const result = await request(`/admin/sites/${siteID}/probe`, { method: 'POST' });
      showToast(result.matched ? '站点探测成功' : '未识别到受支持的站点', result.matched ? 'success' : 'warning');
      await loadSites();
    } catch (error) {
      showToast(apiError(error), 'error');
      await loadSites();
    }
  }

  async function deleteSite(siteID) {
    const site = findSite(siteID);
    if (!site || !window.confirm(`删除站点“${site.name}”？关联账号和投影渠道将被禁用。`)) return;
    try {
      await request(`/admin/sites/${siteID}`, { method: 'DELETE' });
      showToast('站点已删除', 'success');
      await loadSites();
    } catch (error) {
      showToast(apiError(error), 'error');
    }
  }

  async function deleteAccount(accountID) {
    const account = findAccount(accountID);
    if (!account || !window.confirm(`删除账号“${account.label}”？对应投影渠道将被禁用。`)) return;
    try {
      await request(`/admin/site-accounts/${accountID}`, { method: 'DELETE' });
      showToast('账号已删除', 'success');
      await loadSites();
    } catch (error) {
      showToast(apiError(error), 'error');
    }
  }

  async function pollTask(taskID, timeoutMs = 120000) {
    const startedAt = Date.now();
    while (Date.now() - startedAt < timeoutMs) {
      const task = await request(`/admin/site-tasks/${encodeURIComponent(taskID)}`);
      if (terminalTaskStatuses.has(task.status)) return task;
      await new Promise((resolve) => window.setTimeout(resolve, 500));
    }
    throw new Error('任务等待超时');
  }

  async function runAccountTask(accountID, kind, quiet = false) {
    const paths = {
      checkin: 'checkin', refresh: 'refresh', model_refresh: 'models/refresh',
    };
    setAccountBusy(accountID, true);
    try {
      const queued = await request(`/admin/site-accounts/${accountID}/${paths[kind]}`, { method: 'POST' });
      const task = await pollTask(queued.task_id);
      if (!quiet) {
        if (task.status === 'success' || task.status === 'partial') {
          showToast(`${kind === 'checkin' ? '签到' : kind === 'refresh' ? '余额刷新' : '模型刷新'}完成`, 'success');
        } else {
          showToast(apiError(task.error || task.status), 'warning');
        }
      }
      return task;
    } catch (error) {
      if (!quiet) showToast(apiError(error), 'error');
      return { status: 'failed', error: apiError(error) };
    } finally {
      setAccountBusy(accountID, false);
    }
  }

  async function runBulk(kind) {
    const accounts = allAccounts().filter((account) => account.enabled);
    if (!accounts.length) {
      showToast('没有可执行的账号', 'warning');
      return;
    }
    const buttonID = kind === 'checkin' ? 'btn-checkin-all' : 'btn-refresh-all-balances';
    const button = document.getElementById(buttonID);
    const original = button.textContent;
    button.disabled = true;
    button.textContent = `执行中 0/${accounts.length}`;
    let cursor = 0;
    let completed = 0;
    let success = 0;
    const worker = async () => {
      while (cursor < accounts.length) {
        const account = accounts[cursor++];
        const result = await runAccountTask(account.id, kind, true);
        if (result.status === 'success' || result.status === 'partial') success++;
        completed++;
        button.textContent = `执行中 ${completed}/${accounts.length}`;
      }
    };
    await Promise.all(Array.from({ length: Math.min(4, accounts.length) }, worker));
    button.disabled = false;
    button.textContent = original;
    showToast(`完成 ${completed} 个账号，成功 ${success} 个`, success === completed ? 'success' : 'warning');
    await loadSites();
  }

  async function showCheckins(accountID) {
    const account = findAccount(accountID);
    document.getElementById('activity-modal-title').textContent = `${account?.label || '账号'} · 签到记录`;
    const body = document.getElementById('activity-modal-body');
    body.innerHTML = '<div class="sites-loading">加载中…</div>';
    openModal('activity-modal');
    try {
      const attempts = await request(`/admin/site-accounts/${accountID}/checkin-runs?limit=50`);
      body.innerHTML = attempts.length ? `
        <div class="activity-table">
          ${attempts.map((item) => `
            <div class="activity-row">
              <span class="site-badge ${badgeClass(item.status)}">${escapeHTML(statusLabel(item.status))}</span>
              <span>${escapeHTML(item.local_day || '')}</span>
              <span>${escapeHTML(item.reward_text || item.message || item.error_code || '—')}</span>
              <time>${escapeHTML(formatTime(item.finished_at || item.started_at))}</time>
            </div>`).join('')}
        </div>` : '<div class="sites-empty-state">暂无签到记录</div>';
    } catch (error) {
      body.innerHTML = `<div class="sites-empty-state">${escapeHTML(apiError(error))}</div>`;
    }
  }

  function openProjection(accountID) {
    const account = findAccount(accountID);
    document.getElementById('projection-account-id').value = accountID;
    document.getElementById('projection-key').value = 'default';
    document.getElementById('projection-name').value = '';
    document.getElementById('projection-api-key').value = '';
    document.getElementById('projection-api-key').placeholder = account?.credential_type === 'api_key'
      ? '留空使用账号已保存的 API Key' : 'Access Token 账号需要填写 API Key';
    document.getElementById('projection-force').checked = false;
    openModal('projection-modal');
  }

  async function submitProjection(event) {
    event.preventDefault();
    const accountID = document.getElementById('projection-account-id').value;
    const body = {
      projection_key: document.getElementById('projection-key').value.trim() || 'default',
      name: document.getElementById('projection-name').value.trim(),
      api_key: document.getElementById('projection-api-key').value.trim(),
      force: document.getElementById('projection-force').checked,
    };
    try {
      const result = await request(`/admin/site-accounts/${accountID}/project`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
      });
      closeModal('projection-modal');
      const actionLabels = { created: '渠道已创建', updated: '渠道已同步', unchanged: '投影没有变化', conflict: '检测到投影冲突' };
      showToast(actionLabels[result.action] || `投影结果：${result.action}`, result.action === 'conflict' ? 'warning' : 'success');
    } catch (error) {
      showToast(apiError(error), 'error');
    }
  }

  async function loadAnnouncements() {
    const container = document.getElementById('announcement-list');
    container.innerHTML = '<div class="sites-loading">加载中…</div>';
    const unreadOnly = document.getElementById('filter-unread-ann').checked;
    try {
      const payload = await requestPayload(`/admin/announcements?limit=200${unreadOnly ? '&unread=true' : ''}`);
      state.announcements = payload.data || [];
      renderAnnouncements();
      await updateUnreadBadge();
    } catch (error) {
      container.innerHTML = `<div class="sites-empty-state">${escapeHTML(apiError(error))}</div>`;
    }
  }

  async function updateUnreadBadge() {
    try {
      const payload = await requestPayload('/admin/announcements?limit=1&unread=true');
      const count = Number(payload.count || 0);
      const badge = document.getElementById('unread-badge');
      badge.textContent = String(count);
      badge.hidden = count === 0;
    } catch (_) {}
  }

  function renderAnnouncements() {
    const container = document.getElementById('announcement-list');
    if (!state.announcements.length) {
      container.innerHTML = '<div class="sites-empty-state">暂无公告</div>';
      return;
    }
    container.innerHTML = state.announcements.map((item) => {
      const site = findSite(item.site_id);
      const preview = String(item.content_markdown || '').replace(/[#>*_`\[\]]/g, '').trim();
      return `
        <article class="announcement-card level-${escapeHTML(item.level || 'info')} ${item.read_at ? '' : 'unread'}" data-action="open-announcement" data-announcement-id="${item.id}">
          <h3 class="announcement-title">${escapeHTML(item.title)}</h3>
          <p class="announcement-preview">${escapeHTML(preview)}</p>
          <div class="announcement-meta"><span>${escapeHTML(site?.name || `站点 ${item.site_id}`)}</span><time>${escapeHTML(formatTime(item.upstream_updated_at || item.last_seen_at))}</time>${item.read_at ? '' : '<span>未读</span>'}</div>
        </article>`;
    }).join('');
  }

  async function openAnnouncement(id) {
    const item = state.announcements.find((announcement) => announcement.id === Number(id));
    if (!item) return;
    document.getElementById('announcement-modal-title').textContent = item.title || '公告';
    const body = document.getElementById('announcement-modal-body');
    body.innerHTML = '';
    body.className = 'sites-modal-body announcement-markdown';
    if (window.MarkdownRenderer) window.MarkdownRenderer.render(body, item.content_markdown || '');
    else body.textContent = item.content_markdown || '';
    openModal('announcement-modal');
    if (!item.read_at) {
      try {
        await request(`/admin/announcements/${item.id}/read`, { method: 'POST' });
        item.read_at = Date.now();
        renderAnnouncements();
        await updateUnreadBadge();
      } catch (_) {}
    }
  }

  async function refreshAnnouncements() {
    const button = document.getElementById('btn-refresh-announcements');
    button.disabled = true;
    try {
      const queued = await request('/admin/announcements/refresh', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
      });
      const task = await pollTask(queued.task_id);
      showToast(task.status === 'success' ? '公告已刷新' : apiError(task.error || task.status), task.status === 'success' ? 'success' : 'warning');
      await loadAnnouncements();
    } catch (error) {
      showToast(apiError(error), 'error');
    } finally {
      button.disabled = false;
    }
  }

  async function markAllAnnouncementsRead() {
    try {
      await request('/admin/announcements/read-all', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
      });
      showToast('公告已全部标记为已读', 'success');
      await loadAnnouncements();
    } catch (error) {
      showToast(apiError(error), 'error');
    }
  }

  async function loadModels() {
    const container = document.getElementById('model-list');
    container.innerHTML = '<div class="sites-loading">加载中…</div>';
    const query = state.modelAccountID ? `?account_id=${encodeURIComponent(state.modelAccountID)}` : '';
    try {
      state.models = await request(`/admin/site-models${query}`);
      renderModels();
    } catch (error) {
      container.innerHTML = `<div class="sites-empty-state">${escapeHTML(apiError(error))}</div>`;
    }
  }

  function populateAccountFilter() {
    const select = document.getElementById('model-account-filter');
    const current = select.value;
    select.innerHTML = '<option value="">全部账号</option>' + allAccounts().map((account) => {
      const site = findSite(account.site_id);
      return `<option value="${account.id}">${escapeHTML(site?.name || '')} / ${escapeHTML(account.label)}</option>`;
    }).join('');
    select.value = current;
  }

  function renderModels() {
    const container = document.getElementById('model-list');
    const query = state.modelSearch.trim().toLowerCase();
    const models = state.models.filter((item) => !query || item.model.toLowerCase().includes(query));
    if (!models.length) {
      container.innerHTML = '<div class="sites-empty-state">暂无模型</div>';
      return;
    }
    container.innerHTML = models.map((item) => {
      const account = findAccount(item.site_account_id);
      const site = account ? findSite(account.site_id) : null;
      return `
        <div class="model-row">
          <span class="model-name">${escapeHTML(item.model)}</span>
          <span class="model-meta">
            ${item.stale ? '<span class="model-stale">过期</span>' : ''}
            <span class="model-source">${escapeHTML(site?.name || '')} / ${escapeHTML(account?.label || String(item.site_account_id))}</span>
          </span>
        </div>`;
    }).join('');
  }

  function switchTab(name) {
    document.querySelectorAll('.sites-tab').forEach((button) => {
      const active = button.dataset.tab === name;
      button.classList.toggle('active', active);
      button.setAttribute('aria-selected', String(active));
    });
    document.querySelectorAll('.sites-tab-pane').forEach((pane) => {
      const active = pane.id === `tab-${name}`;
      pane.classList.toggle('active', active);
      pane.hidden = !active;
    });
    if (name === 'announcements') loadAnnouncements();
    if (name === 'models') loadModels();
  }

  function bindEvents() {
    document.querySelector('.sites-tab-bar').addEventListener('click', (event) => {
      const button = event.target.closest('.sites-tab');
      if (button) switchTab(button.dataset.tab);
    });
    document.getElementById('btn-add-site').addEventListener('click', () => openSiteForm());
    document.getElementById('btn-add-site-empty').addEventListener('click', () => openSiteForm());
    document.getElementById('btn-checkin-all').addEventListener('click', () => runBulk('checkin'));
    document.getElementById('btn-refresh-all-balances').addEventListener('click', () => runBulk('refresh'));
    document.getElementById('site-search').addEventListener('input', (event) => {
      state.siteSearch = event.target.value;
      renderSites();
    });
    document.getElementById('site-form').addEventListener('submit', submitSiteForm);
    document.getElementById('account-form').addEventListener('submit', submitAccountForm);
    document.getElementById('account-form-credential-type').addEventListener('change', updateCredentialLabel);
    document.getElementById('projection-form').addEventListener('submit', submitProjection);
    document.getElementById('btn-refresh-announcements').addEventListener('click', refreshAnnouncements);
    document.getElementById('btn-mark-all-read').addEventListener('click', markAllAnnouncementsRead);
    document.getElementById('filter-unread-ann').addEventListener('change', loadAnnouncements);
    document.getElementById('model-account-filter').addEventListener('change', (event) => {
      state.modelAccountID = event.target.value;
      loadModels();
    });
    document.getElementById('model-search').addEventListener('input', (event) => {
      state.modelSearch = event.target.value;
      renderModels();
    });

    document.addEventListener('click', async (event) => {
      const target = event.target.closest('[data-action]');
      if (!target) return;
      const { action, siteId, accountId, announcementId } = target.dataset;
      if (action.startsWith('close-')) {
        closeModal(action.slice(6));
        return;
      }
      if (action === 'add-account') openAccountForm(siteId);
      if (action === 'edit-site') openSiteForm(findSite(siteId));
      if (action === 'delete-site') await deleteSite(siteId);
      if (action === 'probe-site') await probeSite(siteId);
      if (action === 'edit-account') {
        const account = findAccount(accountId);
        if (account) openAccountForm(account.site_id, account);
      }
      if (action === 'delete-account') await deleteAccount(accountId);
      if (action === 'checkin-account') { await runAccountTask(accountId, 'checkin'); await loadSites(); }
      if (action === 'refresh-account') { await runAccountTask(accountId, 'refresh'); await loadSites(); }
      if (action === 'refresh-models') { await runAccountTask(accountId, 'model_refresh'); await loadSites(); }
      if (action === 'project-account') openProjection(accountId);
      if (action === 'show-checkins') await showCheckins(accountId);
      if (action === 'open-announcement') await openAnnouncement(announcementId);
    });

    document.addEventListener('keydown', (event) => {
      if (event.key !== 'Escape') return;
      const open = [...document.querySelectorAll('.sites-modal[aria-hidden="false"]')].pop();
      if (open) closeModal(open.id);
    });
  }

  window.initPageBootstrap({
    topbarKey: 'sites',
    run: async () => {
      bindEvents();
      await loadSites();
      updateUnreadBadge();
    },
  });
})();
