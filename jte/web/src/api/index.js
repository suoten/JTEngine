import axios from 'axios'
import router from '../router'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 10000,
  withCredentials: true, // AUTO-FIX-2026-06-30 [P1-7]: 携带 Cookie（CSRF token + SameSite=Strict）
})

// AUTO-FIX-2026-06-30 [P1-7]: 读取 CSRF token Cookie，注入到请求头。
// 后端 CSRFMiddleware 校验 X-CSRF-Token 头与 csrf_token Cookie 一致。
// 登录成功后后端通过 Set-Cookie 下发 csrf_token（SameSite=Strict）。
function getCSRFToken() {
  const match = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]+)/)
  return match ? decodeURIComponent(match[1]) : ''
}

// ===================================================================
// AUTO-FIX-2026-07-02: Token 自动刷新机制
// 401 时自动调用 /auth/refresh 刷新 token，刷新期间暂存请求队列，
// 刷新成功后重试队列中的请求；刷新失败（refresh token 过期）才跳转登录。
// 支持多标签页同步：通过 localStorage 事件协调，仅一个标签页执行刷新。
// ===================================================================

let isRefreshing = false      // 当前标签页是否正在刷新 token
let refreshSubscribers = []   // 刷新期间暂存的请求队列（等待重试）

// 将暂存请求加入队列，返回 Promise（刷新完成后 resolve）
function subscribeTokenRefresh(config) {
  return new Promise((resolve, reject) => {
    refreshSubscribers.push({ config, resolve, reject })
  })
}

// 刷新完成后，重试队列中所有暂存请求
function onRefreshed(newToken) {
  refreshSubscribers.forEach(({ config, resolve, reject }) => {
    if (newToken) {
      config.headers.Authorization = `Bearer ${newToken}`
      resolve(config)
    } else {
      reject(new Error('token refresh failed'))
    }
  })
  refreshSubscribers = []
}

// 刷新失败，拒绝队列中所有暂存请求
function onRefreshFailed(err) {
  refreshSubscribers.forEach(({ reject }) => reject(err))
  refreshSubscribers = []
}

// 执行 token 刷新（单例，避免并发刷新）
async function doRefreshToken() {
  const oldToken = localStorage.getItem('jte_token')
  if (!oldToken) {
    throw new Error('no token to refresh')
  }
  // 使用裸 axios 调用，避免触发自身拦截器导致死循环
  const res = await axios.post('/api/v1/auth/refresh', {}, {
    headers: { Authorization: `Bearer ${oldToken}` },
    withCredentials: true,
    timeout: 10000,
  })
  if (res.data && res.data.code === 0 && res.data.data && res.data.data.token) {
    const newToken = res.data.data.token
    localStorage.setItem('jte_token', newToken)
    // 多标签页同步：通过 storage 事件通知其他标签页 token 已更新
    localStorage.setItem('jte_token_refreshed', JSON.stringify({ token: newToken, ts: Date.now() }))
    // 清理通知标记（短暂延迟后清除，确保其他标签页收到事件）
    setTimeout(() => localStorage.removeItem('jte_token_refreshed'), 500)
    return newToken
  }
  throw new Error('refresh token expired or invalid')
}

// 多标签页同步：监听其他标签页的 token 刷新事件
// 当其他标签页刷新 token 后，本标签页更新本地 token，避免本标签页请求 401
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (e) => {
    if (e.key === 'jte_token_refreshed' && e.newValue) {
      try {
        const { token } = JSON.parse(e.newValue)
        if (token) {
          // 其他标签页已刷新 token，更新本地（不触发刷新流程）
          localStorage.setItem('jte_token', token)
          // 如果本标签页正在刷新，取消（其他标签页已完成）
          if (isRefreshing) {
            isRefreshing = false
            onRefreshed(token)
          }
        }
      } catch (err) { /* ignore */ }
    }
    // 其他标签页登出时，本标签页也清理
    if (e.key === 'jte_token' && !e.newValue && !isRefreshing) {
      if (router.currentRoute.value.path !== '/login') {
        router.push('/login')
      }
    }
  })
}

// 强制跳转登录页（refresh token 过期时调用）
function forceLogout() {
  localStorage.removeItem('jte_token')
  localStorage.removeItem('jte_user')
  // 通知其他标签页登出
  localStorage.setItem('jte_force_logout', JSON.stringify({ ts: Date.now() }))
  setTimeout(() => localStorage.removeItem('jte_force_logout'), 500)
  if (router.currentRoute.value.path !== '/login') {
    router.push('/login')
  }
}

// 监听其他标签页的强制登出事件
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (e) => {
    if (e.key === 'jte_force_logout' && e.newValue) {
      if (router.currentRoute.value.path !== '/login') {
        router.push('/login')
      }
    }
  })
}

api.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('jte_token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    // AUTO-FIX-2026-06-30 [P1-7]: 注入 CSRF token（写操作必须）
    const csrfToken = getCSRFToken()
    if (csrfToken && ['post', 'put', 'patch', 'delete'].includes((config.method || '').toLowerCase())) {
      config.headers['X-CSRF-Token'] = csrfToken
    }
    // AUTO-FIX-2026-07-02: 数据权限 - 列表查询自动附加 data_scope 条件
    // 从 localStorage 读取数据范围（登录时从 /auth/permissions 拉取并缓存）
    if (config.method === 'get' && config.params) {
      const scopeStr = localStorage.getItem('jte_data_scope')
      if (scopeStr) {
        try {
          const ds = JSON.parse(scopeStr)
          if (ds.scope_type === 'org' && ds.org_id && !config.params.org_id) {
            config.params.org_id = ds.org_id
          } else if (ds.scope_type === 'vehicle' && ds.vehicle_ids && ds.vehicle_ids.length > 0 && !config.params.vehicle_ids) {
            config.params.vehicle_ids = ds.vehicle_ids.join(',')
          }
        } catch (err) { /* ignore */ }
      }
    }
    return config
  },
  (error) => Promise.reject(error)
)

api.interceptors.response.use(
  (response) => response.data,
  async (error) => {
    const originalRequest = error.config

    // AUTO-FIX-2026-07-02: 401 时自动刷新 token（而非直接跳登录）
    // 条件：401 + 未在刷新中 + 非刷新请求本身 + 未重试过
    if (error.response && error.response.status === 401 &&
        originalRequest && !originalRequest._isRetry &&
        !originalRequest.url.includes('/auth/refresh') &&
        !originalRequest.url.includes('/auth/login')) {

      // 如果正在刷新中，将请求加入队列等待
      if (isRefreshing) {
        return subscribeTokenRefresh(originalRequest).then((config) => api(config))
      }

      originalRequest._isRetry = true
      isRefreshing = true

      try {
        const newToken = await doRefreshToken()
        isRefreshing = false
        // 重试队列中暂存的请求
        onRefreshed(newToken)
        // 重试当前请求
        originalRequest.headers.Authorization = `Bearer ${newToken}`
        return api(originalRequest)
      } catch (refreshErr) {
        isRefreshing = false
        // 刷新失败，拒绝队列请求并强制登出
        onRefreshFailed(refreshErr)
        forceLogout()
        return Promise.reject(refreshErr)
      }
    }

    // 非 401 错误或刷新请求本身的 401，正常拒绝
    // AUTO-FIX-2026-07-02 [防克隆]: 透传 HTTP 状态码到 error.status，
    // 便于调用方区分 401（密码错误）/429（账户锁定）/其他错误
    console.error('API Error:', error)
    if (error.response) {
      error.status = error.response.status
      // 后端返回的 JSON 体中可能含 code/message，透传到 error
      const body = error.response.data
      if (body && typeof body === 'object') {
        error.message = body.message || error.message
        error.code = body.code
      }
    }
    return Promise.reject(error)
  }
)

export const statsApi = {
  getOverview: () => api.get('/stats/overview'),
  getOnlineCount: () => api.get('/stats/online'),
  getAlarmCount: () => api.get('/stats/alarms'),
}

export const sessionApi = {
  getList: (params) => api.get('/sessions', { params }),
  getDetail: (id) => api.get(`/sessions/${id}`),
}

export const alarmApi = {
  getList: (params) => api.get('/alarms', { params }),
  getStats: () => api.get('/alarms/stats'),
  // AUTO-FIX-2026-07-02: 报警处理闭环 - 详情/确认/派单处理/关闭归档/结果上报终端
  getDetail: (id) => api.get(`/alarms/${id}`),
  getReport: (params) => api.get('/alarms/report', { params }),
  ackAlarm: (id, data) => api.put(`/alarms/${id}/ack`, data),
  processAlarm: (id, data) => api.put(`/alarms/${id}/process`, data),
  closeAlarm: (id, data) => api.put(`/alarms/${id}/close`, data),
  reportToTerminal: (id, data) => api.post(`/alarms/${id}/notify`, data),
}

export const vehicleApi = {
  getList: (params) => api.get('/vehicles', { params }),
  getDetail: (id) => api.get(`/vehicles/${id}`),
  getLocations: () => api.get('/vehicles/locations'),
}

export const logApi = {
  getList: (params) => api.get('/protocol-logs', { params }),
  getDetail: (id) => api.get(`/protocol-logs/${id}`),
}

export const authApi = {
  getStatus: () => api.get('/auth/status'),
  activateLicense: (code) => api.post('/auth/activate', { code }),
  removeLicense: () => api.delete('/auth/license'),
  startTrial: (moduleName) => api.post('/auth/trial', { module_name: moduleName }),
  login: (data) => api.post('/auth/login', data),
  refresh: () => api.post('/auth/refresh'),
  // AUTO-FIX-2026-07-02: 权限动态化 - 登录后从后端拉取当前用户完整权限树 + 数据范围
  // 替代前端硬编码的 ROLE_PERMISSIONS，支持自定义角色 + 数据权限 + 权限实时变更
  getPermissions: () => api.get('/auth/permissions'),
}

export const deviceApi = {
  getList: (params) => api.get('/devices', { params }),
  sendCommand: (data) => api.post('/devices/command', data),
}

export const trackApi = {
  getTrack: (params) => api.get('/tracks', { params }),
  getLatestLocations: () => api.get('/vehicles/latest-locations'),
}

export const videoApi = {
  startStream: (data) => api.post('/media/start', data),
  stopStream: (data) => api.post('/media/stop', data),
  webrtc: (data) => api.post('/media/webrtc', data),
  ptz: (data) => api.post('/media/ptz', data),
  playback: (data) => api.post('/media/playback', data),
  download: (data) => api.post('/media/download', data),
  getStreams: () => api.get('/media/streams'),
  // AUTO-FIX-2026-06-29 [P0-2]: 视频质量聚合接口（后端 /video/quality 别名于 /media/quality）
  // 支持按 deviceId + channel 过滤；返回所有活跃流的码率/帧率/丢包率/在线状态
  getQuality: (params) => api.get('/video/quality', { params }),
  // AUTO-FIX-2026-06-26: 第三轮视频监控修复 - 关键帧请求 API
  keyframe: (data) => api.post('/media/keyframe', data),
  // RTP 传输模式切换（UDP/TCP，公网/NAT环境用TCP）
  setStreamMode: (data) => api.post('/media/stream-mode', data),
  getStreamMode: (params) => api.get('/media/stream-mode', { params }),
  // AUTO-FIX-2026-07-02: 双码流切换 - 主码流(0)/子码流(1)手动切换，保留播放状态
  switchStream: (data) => api.post('/media/switch-stream', data),
  // AUTO-FIX-2026-07-02: 录制断片查询/合并
  getFragments: (params) => api.get('/media/fragments', { params }),
  mergeFragments: (data) => api.post('/media/fragments/merge', data),
  getRecords: (params) => api.get('/media/records', { params }),
  screenshot: (data) => api.post('/media/screenshot', data),
}

export const reportApi = {
  generate: (data) => api.post('/reports/generate', data),
  getList: (params) => api.get('/reports', { params }),
}

export const cascadeApi = {
  getPlatforms: () => api.get('/cascade/platforms'),
  createPlatform: (data) => api.post('/cascade/platforms', data),
  updatePlatform: (id, data) => api.put(`/cascade/platforms/${id}`, data),
  deletePlatform: (id) => api.delete(`/cascade/platforms/${id}`),
  // AUTO-FIX-2026-07-02: 在线 809 会话状态（实时在线/离线/最后心跳）
  getPlatformStatus: (id) => api.get(`/platforms/${id}/status`),
  getOnlinePlatforms: () => api.get('/platforms'),
}

// AUTO-FIX-2026-07-02: 转发规则管理（按车辆/消息类型配置下级→上级平台转发规则）
export const forwardRuleApi = {
  getList: (params) => api.get('/forward-rules', { params }),
  create: (data) => api.post('/forward-rules', data),
  get: (id) => api.get(`/forward-rules/${id}`),
  update: (id, data) => api.put(`/forward-rules/${id}`, data),
  delete: (id) => api.delete(`/forward-rules/${id}`),
}

export const systemApi = {
  getStatus: () => api.get('/system/status'),
  getModules: () => api.get('/system/modules'),
  getUsers: () => api.get('/system/users'),
  getConfig: () => api.get('/system/config'),
}

// 存储分层管理：统计 / TTL / 归档 / 缓存命中率
export const storageApi = {
  getStats: () => api.get('/storage/stats'),
  getTtl: () => api.get('/storage/ttl'),
  updateTtl: (data) => api.put('/storage/ttl', data),
  getArchiveStatus: () => api.get('/storage/archive/status'),
  triggerArchive: (data) => api.post('/storage/archive/trigger', data),
  getCacheHitrate: (params) => api.get('/storage/cache/hitrate', { params }),
  // AUTO-FIX-2026-06-30 [P1-9]: TDengine 集群状态（VGroups/Replica/dnodes）
  getClusterStatus: () => api.get('/storage/cluster/status'),
  // AUTO-FIX-2026-06-30 [P1-9]: 冷热分层数据量监控
  getTierStats: () => api.get('/storage/tier/stats'),
  // AUTO-FIX-2026-07-02: 归档任务实时进度 + 上次执行结果
  getArchiveProgress: () => api.get('/storage/archive/progress'),
}

// 电子围栏管理
export const geofenceApi = {
  getList: (params) => api.get('/geofences', { params }),
  create: (data) => api.post('/geofences', data),
  update: (id, data) => api.put(`/geofences/${id}`, data),
  delete: (id) => api.delete(`/geofences/${id}`),
}

// 驾驶员管理
export const driverApi = {
  getList: (params) => api.get('/drivers', { params }),
  create: (data) => api.post('/drivers', data),
  update: (id, data) => api.put(`/drivers/${id}`, data),
  delete: (id) => api.delete(`/drivers/${id}`),
}

// AUTO-FIX-2026-06-26: 地图API Key配置化
export const configApi = {
  getMapConfig: () => api.get('/config/map'),
}

// AUTO-FIX-2026-06-26: 第六轮遗留修复 - 官网信息接口（购买链接）
export const websiteApi = {
  getInfo: () => api.get('/website/info'),
}

export const aiApi = {
  analyzeAlarm: (data) => api.post('/ai/analyze-alarm', data),
  checkFatigue: (params) => api.get('/ai/driver-fatigue', { params }),
  getRiskScore: (params) => api.get('/ai/risk-score', { params }),
  // AUTO-FIX-2026-06-26: 第六轮前端修复 - AI 助手对话接口（Nlp.vue 调用 aiApi.chat 但未定义）
  chat: (data) => api.post('/ai/chat', data),
  nl2sql: (data) => api.post('/ai/nl2sql', data),
  generateReport: (data) => api.post('/ai/generate-report', data),
  debugProtocol: (data) => api.post('/ai/debug-protocol', data),
  queryKnowledge: (params) => api.get('/ai/knowledge', { params }),
}

// AUTO-FIX-2026-06-26: 第六轮遗留修复 - AI 助手 WebSocket 实时对话（后端已实现 ChatWS）
// 鉴权通过 query token 传递（WebSocket 无法设置自定义 Authorization Header）
// AUTO-FIX-2026-07-05: 增加断连自动重连逻辑（指数退避，最大30秒）
export function createAIChatSocket() {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const token = localStorage.getItem('jte_token') || ''
  const url = `${proto}//${window.location.host}/api/v1/ai/chat/ws?token=${encodeURIComponent(token)}`
  return new WebSocket(url)
}

// 带自动重连的 AI 聊天 WebSocket
// 使用指数退避（1s→2s→4s→...→30s），网络恢复后自动重连
export function createReconnectingAIChatSocket(maxRetries = 10) {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const token = localStorage.getItem('jte_token') || ''
  const baseUrl = `${proto}//${window.location.host}/api/v1/ai/chat/ws?token=${encodeURIComponent(token)}`

  let ws = null
  let retryCount = 0
  let retryTimer = null
  let manuallyClosed = false
  const listeners = { open: [], message: [], close: [], error: [] }

  function connect() {
    ws = new WebSocket(baseUrl)

    ws.onopen = (event) => {
      retryCount = 0
      listeners.open.forEach(cb => cb(event))
    }

    ws.onmessage = (event) => {
      listeners.message.forEach(cb => cb(event))
    }

    ws.onerror = (event) => {
      listeners.error.forEach(cb => cb(event))
    }

    ws.onclose = (event) => {
      listeners.close.forEach(cb => cb(event))
      if (!manuallyClosed && retryCount < maxRetries) {
        const delay = Math.min(1000 * Math.pow(2, retryCount), 30000)
        retryCount++
        retryTimer = setTimeout(connect, delay)
      }
    }
  }

  connect()

  return {
    send: (data) => ws && ws.readyState === WebSocket.OPEN ? ws.send(data) : false,
    close: () => {
      manuallyClosed = true
      if (retryTimer) clearTimeout(retryTimer)
      if (ws) ws.close()
    },
    get readyState() { return ws ? ws.readyState : WebSocket.CLOSED },
    addEventListener: (type, callback) => {
      if (listeners[type]) listeners[type].push(callback)
    },
    removeEventListener: (type, callback) => {
      if (listeners[type]) {
        listeners[type] = listeners[type].filter(cb => cb !== callback)
      }
    },
    get retryCount() { return retryCount },
  }
}

// AUTO-FIX-2026-07-02: JT/T 905 出租车专用接口
// 905 协议营运数据通过协议层入库，复用 vehicle/device/track 通用接口
// 营运状态/计价器数据通过 vehicleApi.getDetail 附加字段获取
// 调度指令通过 deviceApi.sendCommand 下发（0x8103 指令）
export const taxiApi = {
  // 营运状态列表（复用车辆列表，附加 905 营运数据）
  getFleetStatus: (params) => api.get('/vehicles', { params }),
  // 车辆详情（含计价器数据、营运状态）
  getVehicleDetail: (id) => api.get(`/vehicles/${id}`),
  // 调度指令下发（0x8103 文本指令 / 0x8300 广告下发）
  sendDispatch: (data) => api.post('/devices/command', data),
  // 实时位置
  getLocations: () => api.get('/vehicles/latest-locations'),
}

export default api
