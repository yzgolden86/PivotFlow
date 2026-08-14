(function () {
  const auth = window.PivotFlowAuth;
  const form = document.getElementById('login-form');
  const password = document.getElementById('password');
  const togglePassword = document.getElementById('toggle-password');
  const loginButton = document.getElementById('login-button');
  const errorMessage = document.getElementById('error-message');
  const params = new URLSearchParams(window.location.search);
  const redirect = auth.getSafeConsolePath(params.get('redirect'), window.location.origin);

  if (auth.hasUsableSession(localStorage)) {
    window.location.replace(redirect);
    return;
  }
  auth.clearSession(localStorage);

  function setLoading(loading) {
    loginButton.disabled = loading;
    password.disabled = loading;
    loginButton.classList.toggle('is-loading', loading);
  }
  function showError(message) { errorMessage.textContent = message; errorMessage.hidden = false; }
  function hideError() { errorMessage.hidden = true; errorMessage.textContent = ''; }

  togglePassword.addEventListener('click', function () {
    const visible = password.type === 'text';
    password.type = visible ? 'password' : 'text';
    togglePassword.setAttribute('aria-pressed', visible ? 'false' : 'true');
    togglePassword.setAttribute('aria-label', visible ? '显示密码' : '隐藏密码');
    password.focus();
  });
  password.addEventListener('input', hideError);

  form.addEventListener('submit', async function (event) {
    event.preventDefault();
    hideError();
    if (!password.value) { showError('请输入管理密码'); password.focus(); return; }
    setLoading(true);
    try {
      const response = await fetch('/login', {
        method: 'POST', cache: 'no-store', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ mode: 'admin', password: password.value }),
      });
      const payload = await response.json().catch(function () { return null; });
      if (!response.ok || !payload || !payload.success || !payload.data || !payload.data.token) {
        const details = payload && payload.data && payload.data.message;
        throw new Error(details || (payload && payload.error) || '密码错误，请重新输入');
      }
      auth.storeAdminSession(localStorage, payload.data);
      loginButton.classList.add('is-success');
      window.setTimeout(function () { window.location.replace(redirect); }, 180);
    } catch (error) {
      showError(error instanceof Error ? error.message : '登录失败，请稍后重试');
      password.select();
      setLoading(false);
    }
  });

  const initialError = params.get('error');
  if (initialError) showError(initialError);
})();
