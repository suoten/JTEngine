import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// AUTO-FIX-2026-07-02: License 状态联动单元测试
// 验证：激活授权码后自动刷新 modules、fetchStatus 更新 activeModules

const mockGetStatus = vi.fn()
const mockActivateLicense = vi.fn()
const mockRemoveLicense = vi.fn()
const mockStartTrial = vi.fn()

vi.mock('../api', () => ({
  authApi: {
    getStatus: (...args) => mockGetStatus(...args),
    activateLicense: (...args) => mockActivateLicense(...args),
    removeLicense: (...args) => mockRemoveLicense(...args),
    startTrial: (...args) => mockStartTrial(...args),
    login: vi.fn(),
    getPermissions: vi.fn(),
    refresh: vi.fn(),
  },
}))

import { useAuthStore } from './auth'

describe('auth store - License 状态联动', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  describe('fetchStatus - 拉取模块授权状态', () => {
    it('应调用 /auth/status 并更新 activeModules', async () => {
      const store = useAuthStore()

      mockGetStatus.mockResolvedValue({
        code: 0,
        data: {
          machine_fingerprint: 'fp-123',
          licenses: [{ id: 'lic-1', modules: ['protocol_809', 'ai'], expires_at: '2027-01-01' }],
          active_modules: ['protocol_809', 'ai'],
          trials: {},
        },
      })

      await store.fetchStatus()

      expect(mockGetStatus).toHaveBeenCalledTimes(1)
      expect(store.machineFingerprint).toBe('fp-123')
      expect(store.licenses).toHaveLength(1)
      expect(store.activeModules).toEqual(['protocol_809', 'ai'])
      expect(store.loaded).toBe(true)
    })

    it('接口失败时 loaded 仍应设为 true（避免阻塞路由）', async () => {
      const store = useAuthStore()

      mockGetStatus.mockRejectedValue(new Error('network error'))

      await store.fetchStatus()

      expect(store.loaded).toBe(true)
      expect(store.activeModules).toEqual([])
    })
  })

  describe('activateLicense - 激活授权码后自动刷新', () => {
    it('激活成功后应调用 fetchStatus 刷新模块状态', async () => {
      const store = useAuthStore()

      mockActivateLicense.mockResolvedValue({ code: 0, data: {} })
      mockGetStatus.mockResolvedValue({
        code: 0,
        data: {
          machine_fingerprint: 'fp-123',
          licenses: [{ id: 'lic-new', modules: ['protocol_809', 'protocol_1078', 'ai'], expires_at: '2027-01-01' }],
          active_modules: ['protocol_809', 'protocol_1078', 'ai'],
          trials: {},
        },
      })

      // 初始状态：无授权模块
      expect(store.activeModules).toEqual([])

      await store.activateLicense('NEW-LICENSE-KEY')

      expect(mockActivateLicense).toHaveBeenCalledWith('NEW-LICENSE-KEY')
      expect(mockGetStatus).toHaveBeenCalledTimes(1) // activateLicense 内部触发
      expect(store.activeModules).toEqual(['protocol_809', 'protocol_1078', 'ai'])
      expect(store.licenses).toHaveLength(1)
    })

    it('激活失败应抛出异常且不刷新模块状态', async () => {
      const store = useAuthStore()
      store.activeModules = ['existing-module']

      mockActivateLicense.mockResolvedValue({
        code: 400,
        message: 'invalid license key',
      })

      await expect(store.activateLicense('BAD-KEY')).rejects.toThrow('invalid license key')
      expect(mockGetStatus).not.toHaveBeenCalled()
      // 原有模块状态不变
      expect(store.activeModules).toEqual(['existing-module'])
    })
  })

  describe('removeLicense - 移除授权后自动刷新', () => {
    it('移除成功后应调用 fetchStatus 刷新模块状态', async () => {
      const store = useAuthStore()
      store.activeModules = ['protocol_809', 'ai']

      mockRemoveLicense.mockResolvedValue({ code: 0 })
      mockGetStatus.mockResolvedValue({
        code: 0,
        data: {
          machine_fingerprint: 'fp-123',
          licenses: [],
          active_modules: [], // 移除后无授权模块
          trials: {},
        },
      })

      await store.removeLicense('lic-1')

      expect(mockRemoveLicense).toHaveBeenCalledWith('lic-1')
      expect(mockGetStatus).toHaveBeenCalledTimes(1)
      expect(store.activeModules).toEqual([])
    })
  })

  describe('startTrial - 开启试用后自动刷新', () => {
    it('试用开启成功后应刷新模块状态', async () => {
      const store = useAuthStore()
      store.activeModules = []

      mockStartTrial.mockResolvedValue({ code: 0 })
      mockGetStatus.mockResolvedValue({
        code: 0,
        data: {
          machine_fingerprint: 'fp-123',
          licenses: [],
          active_modules: ['protocol_809'], // 试用模块加入 active
          trials: {
            protocol_809: { expires_at: '2026-07-09T00:00:00Z' },
          },
        },
      })

      await store.startTrial('protocol_809')

      expect(mockStartTrial).toHaveBeenCalledWith('protocol_809')
      expect(mockGetStatus).toHaveBeenCalledTimes(1)
      expect(store.activeModules).toEqual(['protocol_809'])
      expect(store.trials.protocol_809).toBeDefined()
    })
  })

  describe('getters - 模块状态查询', () => {
    it('hasModule 应正确检查模块授权', () => {
      const store = useAuthStore()
      store.activeModules = ['protocol_809', 'ai']

      expect(store.hasModule('protocol_809')).toBe(true)
      expect(store.hasModule('ai')).toBe(true)
      expect(store.hasModule('protocol_1078')).toBe(false)
    })

    it('isFree 应在无授权模块时返回 true', () => {
      const store = useAuthStore()
      store.activeModules = []

      expect(store.isFree).toBe(true)

      store.activeModules = ['protocol_809']
      expect(store.isFree).toBe(false)
    })

    it('getModuleStatus 应返回正确的模块状态', () => {
      const store = useAuthStore()
      store.licenses = [{
        modules: ['protocol_809'],
        expires_at: '2027-01-01',
        expired: false,
      }]
      store.trials = {
        ai: { expires_at: '2026-07-09T00:00:00Z' },
      }

      expect(store.getModuleStatus('protocol_809')).toBe('licensed')
      expect(store.getModuleStatus('ai')).toBe('trial')
      expect(store.getModuleStatus('protocol_1078')).toBe('unlicensed')
    })

    it('getModuleStatus 应识别即将过期的授权', () => {
      const store = useAuthStore()
      // 6 天后过期（< 7 天阈值）
      const sixDaysLater = new Date(Date.now() + 6 * 24 * 60 * 60 * 1000).toISOString()
      store.licenses = [{
        modules: ['protocol_809'],
        expires_at: sixDaysLater,
        expired: false,
      }]

      expect(store.getModuleStatus('protocol_809')).toBe('expiring_soon')
    })

    it('getModuleStatus 应识别已过期的授权', () => {
      const store = useAuthStore()
      store.licenses = [{
        modules: ['protocol_809'],
        expires_at: '2020-01-01',
        expired: true,
      }]

      expect(store.getModuleStatus('protocol_809')).toBe('expired')
    })

    it('getTrialRemainingDays 应返回剩余试用天数', () => {
      const store = useAuthStore()
      const fiveDaysLater = new Date(Date.now() + 5 * 24 * 60 * 60 * 1000).toISOString()
      store.trials = {
        ai: { expires_at: fiveDaysLater },
      }

      const days = store.getTrialRemainingDays('ai')
      expect(days).toBeGreaterThanOrEqual(4)
      expect(days).toBeLessThanOrEqual(5)
    })

    it('getTrialRemainingDays 无试用时应返回 0', () => {
      const store = useAuthStore()
      store.trials = {}

      expect(store.getTrialRemainingDays('ai')).toBe(0)
    })
  })
})
