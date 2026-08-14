(function (root, factory) {
  const api = factory();
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (root) root.PivotFlowAuth = api;
})(typeof window !== 'undefined' ? window : globalThis, function () {
  const TOKEN_KEY = 'pivotflow_token';
  const EXPIRY_KEY = 'pivotflow_token_expiry';
  const ROLE_KEY = 'pivotflow_web_role';
  const CONSOLE_PATH = '/web/console/';

  function clearSession(storage) {
    storage.removeItem(TOKEN_KEY);
    storage.removeItem(EXPIRY_KEY);
    storage.removeItem(ROLE_KEY);
    storage.removeItem('pivotflow_api_token');
  }

  function storeAdminSession(storage, data, now = Date.now()) {
    clearSession(storage);
    storage.setItem(TOKEN_KEY, data.token);
    storage.setItem(EXPIRY_KEY, String(now + Number(data.expiresIn || 0) * 1000));
    storage.setItem(ROLE_KEY, 'admin');
  }

  function hasUsableSession(storage, now = Date.now()) {
    const token = storage.getItem(TOKEN_KEY);
    const expiry = Number(storage.getItem(EXPIRY_KEY) || 0);
    return Boolean(token) && (expiry <= 0 || now < expiry);
  }

  function getSafeConsolePath(redirect, origin) {
    if (!redirect || typeof redirect !== 'string') return CONSOLE_PATH;
    const candidate = redirect.trim();
    if (!candidate.startsWith('/') || candidate.startsWith('//')) return CONSOLE_PATH;
    try {
      const url = new URL(candidate, origin);
      if (url.origin !== origin) return CONSOLE_PATH;
      if (url.pathname !== '/web/console' && !url.pathname.startsWith('/web/console/')) return CONSOLE_PATH;
      return `${CONSOLE_PATH}${url.search}${url.hash}`;
    } catch (_) {
      return CONSOLE_PATH;
    }
  }

  return { TOKEN_KEY, EXPIRY_KEY, ROLE_KEY, CONSOLE_PATH, clearSession, storeAdminSession, hasUsableSession, getSafeConsolePath };
});
