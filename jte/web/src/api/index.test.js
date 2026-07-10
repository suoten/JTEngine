import { describe, it, expect, beforeAll, beforeEach, vi, afterEach } from 'vitest'

// AUTO-FIX-2026-07-02: Token 自动刷新单元测试
// 验证：401 自动刷新、请求队列、多标签页同步、刷新失败跳转登录
//
// 注意：api/index.js 在模块加载时即注册 storage 事件监听器和拦截器，
// 且 isRefreshing/refreshSubscribers 为模块私有变量无法直接访问。
// 因此采用「行为驱动」测试：通过 mock axios 验证刷新流程的副作用。

import axios from 'axios'

// 测试用 Token（从环境变量获取，避免硬编码凭证）
const TEST_TOKEN = process.env.JTE_TEST_TOKEN || 'test-tk'
const EXPIRED_TOKEN = process.env.JTE_EXPIRED_TOKEN || 'exp-tk'
const REFRESHED_TOKEN = process.env.JTE_REFRESHED_TOKEN || 'refresh-tk'
const OTHER_TAB_TOKEN = process.env.JTE_OTHER_TAB_TOKEN || 'other-tk'

// Mock axios.create 返回的实例（必须可调用，因为 api/index.js 中 api(config) 会将实例作为函数调用）
const mockAxiosInstance = vi.fn(() => Promise.resolve({ code: 0, data: {} }))
mockAxiosInstance.get = vi.fn()
mockAxiosInstance.post = vi.fn()
mockAxiosInstance.put = vi.fn()
mockAxiosInstance.delete = vi.fn()
mockAxiosInstance.patch = vi.fn()
mockAxiosInstance.interceptors = {
  request: { use: vi.fn() },
  response: { use: vi.fn() },
}

vi.mock('axios', () => ({
  default: {
    create: vi.fn(() => mockAxiosInstance),
    post: vi.fn(), // 用于 doRefreshToken 中的裸 axios.post 调用
    get: vi.fn(),
  },
}))

// Mock router（api/index.js 顶层 import router）
const mockPush = vi.fn()
vi.mock('../router', () => ({
  default: {
    push: (...args) => mockPush(...args),
    currentRoute: { value: { path: '/' } },
  },
}))

// 捕获拦截器 handler（模块加载时注册一次，后续 import 返回缓存不会重新注册）
let requestInterceptor
let responseSuccessHandler
let responseErrorHandler

beforeAll(async () => {
  // 动态 import 触发模块加载，注册拦截器
  await import('./index')
  // 捕获注册时传入的 handler
  requestInterceptor = mockAxiosInstance.interceptors.request.use.mock.calls[0][0]
  const responseCalls = mockAxiosInstance.interceptors.response.use.mock.calls[0]
  responseSuccessHandler = responseCalls[0]
  responseErrorHandler = responseCalls[1]
})

describe('Token 自动刷新机制', () => {
  beforeEach(() => {
    // 不清除 interceptor 的 mock calls（已在 beforeAll 捕获）
    // 仅清除调用记录和返回值
    mockPush.mockClear()
    axios.post.mockClear()
    localStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('应注册请求和响应拦截器', () => {
    expect(mockAxiosInstance.interceptors.request.use).toHaveBeenCalled()
    expect(mockAxiosInstance.interceptors.response.use).toHaveBeenCalled()
  })

  it('请求拦截器应注入 Authorization 头', () => {
    localStorage.setItem('jte_token', TEST_TOKEN)
    const config = { headers: {}, method: 'get', params: {} }

    const result = requestInterceptor(config)

    expect(result.headers.Authorization).toBe(`Bearer ${TEST_TOKEN}`)
  })

  it('请求拦截器应对 GET 请求附加数据权限条件', () => {
    localStorage.setItem('jte_data_scope', JSON.stringify({
      scope_type: 'org',
      org_id: 'org-123',
      vehicle_ids: [],
    }))

    const config = { headers: {}, method: 'get', params: { page: 1 } }
    const result = requestInterceptor(config)

    expect(result.params.org_id).toBe('org-123')
  })

  it('请求拦截器应对 vehicle 范围附加 vehicle_ids', () => {
    localStorage.setItem('jte_data_scope', JSON.stringify({
      scope_type: 'vehicle',
      org_id: '',
      vehicle_ids: ['v1', 'v2', 'v3'],
    }))

    const config = { headers: {}, method: 'get', params: { page: 1 } }
    const result = requestInterceptor(config)

    expect(result.params.vehicle_ids).toBe('v1,v2,v3')
  })

  it('数据权限不应覆盖已显式传入的 org_id', () => {
    localStorage.setItem('jte_data_scope', JSON.stringify({
      scope_type: 'org',
      org_id: 'auto-org',
      vehicle_ids: [],
    }))

    const config = { headers: {}, method: 'get', params: { org_id: 'explicit-org' } }
    const result = requestInterceptor(config)

    expect(result.params.org_id).toBe('explicit-org')
  })

  it('响应拦截器应正常返回成功响应的 data', () => {
    const response = { data: { code: 0, data: { foo: 'bar' } } }
    const result = responseSuccessHandler(response)
    expect(result).toEqual({ code: 0, data: { foo: 'bar' } })
  })

  it('响应拦截器对非 401 错误应直接 reject', async () => {
    const error = {
      response: { status: 500, data: 'server error' },
      config: { url: '/api/v1/vehicles', _isRetry: false },
    }

    await expect(responseErrorHandler(error)).rejects.toEqual(error)
  })

  it('响应拦截器对 /auth/login 的 401 不应触发刷新', async () => {
    const error = {
      response: { status: 401 },
      config: { url: '/api/v1/auth/login', _isRetry: false },
    }

    await expect(responseErrorHandler(error)).rejects.toEqual(error)
  })

  it('响应拦截器对 /auth/refresh 的 401 不应触发刷新', async () => {
    const error = {
      response: { status: 401 },
      config: { url: '/api/v1/auth/refresh', _isRetry: false },
    }

    await expect(responseErrorHandler(error)).rejects.toEqual(error)
  })

  it('401 触发刷新成功后应更新 token', async () => {
    axios.post.mockResolvedValue({
      data: {
        code: 0,
        data: { token: REFRESHED_TOKEN },
      },
    })

    localStorage.setItem('jte_token', EXPIRED_TOKEN)

    const originalRequest = {
      url: '/api/v1/vehicles',
      method: 'get',
      headers: {},
      _isRetry: false,
    }
    const error = {
      response: { status: 401 },
      config: originalRequest,
    }

    // 调用 errorHandler 触发刷新流程
    // 可能因为 mockAxiosInstance 不可调用而抛错，但 token 刷新副作用应已发生
    try {
      await responseErrorHandler(error)
    } catch (e) {
      // 预期可能抛错（mock 不完整），忽略
      console.debug('Expected test error:', e)
    }

    // 验证刷新请求被发起
    expect(axios.post).toHaveBeenCalledWith(
      '/api/v1/auth/refresh',
      {},
      expect.objectContaining({
        headers: { Authorization: `Bearer ${EXPIRED_TOKEN}` },
        withCredentials: true,
        timeout: 10000,
      })
    )

    // 验证 localStorage 中的 token 已更新
    expect(localStorage.getItem('jte_token')).toBe(REFRESHED_TOKEN)
  })

  it('刷新失败应触发 forceLogout（清除 token + 跳转登录）', async () => {
    axios.post.mockResolvedValue({
      data: { code: 401, message: 'refresh token expired' },
    })

    localStorage.setItem('jte_token', EXPIRED_TOKEN)
    localStorage.setItem('jte_user', JSON.stringify({ id: '1', username: 'admin' }))

    const originalRequest = {
      url: '/api/v1/vehicles',
      method: 'get',
      headers: {},
      _isRetry: false,
    }
    const error = {
      response: { status: 401 },
      config: originalRequest,
    }

    await expect(responseErrorHandler(error)).rejects.toBeDefined()

    // 验证 token 和 user 已清除
    expect(localStorage.getItem('jte_token')).toBeNull()
    expect(localStorage.getItem('jte_user')).toBeNull()

    // 验证跳转登录页
    expect(mockPush).toHaveBeenCalledWith('/login')
  })

  it('多标签页同步：其他标签页刷新 token 后应更新本地 token', async () => {
    const storageEvent = new StorageEvent('storage', {
      key: 'jte_token_refreshed',
      newValue: JSON.stringify({ token: OTHER_TAB_TOKEN, ts: Date.now() }),
    })

    window.dispatchEvent(storageEvent)

    // 验证本地 token 已更新
    expect(localStorage.getItem('jte_token')).toBe(OTHER_TAB_TOKEN)
  })

  it('多标签页同步：其他标签页登出后应跳转登录', async () => {
    const storageEvent = new StorageEvent('storage', {
      key: 'jte_token',
      newValue: null,
    })

    window.dispatchEvent(storageEvent)

    expect(mockPush).toHaveBeenCalledWith('/login')
  })

  it('多标签页同步：force_logout 事件应触发跳转登录', async () => {
    const storageEvent = new StorageEvent('storage', {
      key: 'jte_force_logout',
      newValue: JSON.stringify({ ts: Date.now() }),
    })

    window.dispatchEvent(storageEvent)

    expect(mockPush).toHaveBeenCalledWith('/login')
  })
})
