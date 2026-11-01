// 只用于让浏览器识别为可安装应用：不拦截请求、不做缓存，数据始终来自服务端。
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
