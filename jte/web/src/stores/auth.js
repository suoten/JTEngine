import { defineStore } from 'pinia'
import { authApi } from '../api'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    machineFingerprint: '',
    licenses: [],
    activeModules: [],
    trials: {},
    loaded: false,
  }),

  getters: {
    hasModule: (state) => (moduleName) => {
      return state.activeModules.includes(moduleName)
    },
    isFree: (state) => state.activeModules.length === 0,
    hasProtocol809: (state) => state.activeModules.includes('809') || state.activeModules.includes('protocol_809'),
    hasProtocol1045: (state) => state.activeModules.includes('1045') || state.activeModules.includes('protocol_1045'),
    hasStorage: (state) => state.activeModules.includes('storage') || state.activeModules.includes('db_storage'),
    hasAI: (state) => state.activeModules.includes('ai'),
    hasAINLP: (state) => state.activeModules.includes('ai_nlp'),
    getModuleStatus: (state) => (moduleName) => {
      for (const lic of state.licenses) {
        if (lic.modules && lic.modules.includes(moduleName)) {
          if (lic.expired) return 'expired'
          const expiresAt = new Date(lic.expires_at)
          const daysLeft = (expiresAt - new Date()) / (1000 * 60 * 60 * 24)
          if (daysLeft < 7) return 'expiring_soon'
          return 'licensed'
        }
      }
      const trial = state.trials[moduleName]
      if (trial) {
        if (new Date(trial.expires_at) < new Date()) return 'trial_expired'
        return 'trial'
      }
      return 'unlicensed'
    },
    getTrialRemainingDays: (state) => (moduleName) => {
      const trial = state.trials[moduleName]
      if (!trial) return 0
      const days = (new Date(trial.expires_at) - new Date()) / (1000 * 60 * 60 * 24)
      return Math.max(0, Math.ceil(days))
    },
  },

  actions: {
    async fetchStatus() {
      try {
        const res = await authApi.getStatus()
        if (res.code === 0 && res.data) {
          this.machineFingerprint = res.data.machine_fingerprint || ''
          this.licenses = res.data.licenses || []
          this.activeModules = res.data.active_modules || []
          this.trials = res.data.trials || {}
        }
        this.loaded = true
      } catch (e) {
        console.error('Failed to fetch auth status:', e)
        this.loaded = true
      }
    },

    async activateLicense(licenseKey) {
      try {
        const res = await authApi.activateLicense(licenseKey)
        if (res.code === 0) {
          await this.fetchStatus()
          return true
        }
        throw new Error(res.message || 'Activation failed')
      } catch (e) {
        console.error('Failed to activate license:', e)
        throw e
      }
    },

    async removeLicense(licenseId) {
      try {
        const res = await authApi.removeLicense(licenseId)
        if (res.code === 0) {
          await this.fetchStatus()
          return true
        }
        throw new Error(res.message || 'Remove failed')
      } catch (e) {
        console.error('Failed to remove license:', e)
        throw e
      }
    },

    async startTrial(moduleName) {
      try {
        const res = await authApi.startTrial(moduleName)
        if (res.code === 0) {
          await this.fetchStatus()
          return true
        }
        throw new Error(res.message || 'Trial start failed')
      } catch (e) {
        console.error('Failed to start trial:', e)
        throw e
      }
    },
  },
})