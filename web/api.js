// REST 客户端。服务端设置了访问令牌时，页面以 #token=... 打开，令牌存入 sessionStorage 后从地址栏去掉。

const TOKEN_KEY = 'essaypilot-token';
let token = '';

(function readToken() {
  try { token = sessionStorage.getItem(TOKEN_KEY) || ''; } catch { /* 隐私模式等 */ }
  const m = location.hash.match(/(?:^#|&)token=([^&]*)/);
  if (m) {
    token = decodeURIComponent(m[1]);
    try { sessionStorage.setItem(TOKEN_KEY, token); } catch { /* ignore */ }
    const rest = location.hash.replace(/(^#|&)token=[^&]*/, '$1').replace(/^#&/, '#').replace(/&$/, '');
    try { history.replaceState(null, '', location.pathname + location.search + (rest === '#' ? '' : rest)); } catch { /* ignore */ }
  }
})();

export function eventsURL() {
  return '/api/events' + (token ? '?token=' + encodeURIComponent(token) : '');
}

export async function api(method, path, body, opts = {}) {
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers.Authorization = 'Bearer ' + token;
  if (opts.actor) headers['X-Actor'] = opts.actor;
  const res = await fetch('/api' + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { data = text; }
  }
  if (!res.ok) {
    const err = new Error((data && data.error) || `${res.status} ${res.statusText}`);
    err.status = res.status;
    throw err;
  }
  return data;
}

export const get = (p, o) => api('GET', p, undefined, o);
export const post = (p, b, o) => api('POST', p, b ?? {}, o);
export const patch = (p, b, o) => api('PATCH', p, b, o);
export const put = (p, b, o) => api('PUT', p, b, o);
export const del = (p, o) => api('DELETE', p, undefined, o);

export function qs(obj) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(obj)) if (v !== undefined && v !== null && v !== '') p.set(k, v);
  const s = p.toString();
  return s ? '?' + s : '';
}
