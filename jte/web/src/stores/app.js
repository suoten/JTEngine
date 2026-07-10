import { defineStore } from 'pinia'

export const useAppStore = defineStore('app', {
  state: () => ({
    sidebarCollapsed: false,
    mapProvider: 'tianditu',
    tiandituToken: '',
  }),

  actions: {
    toggleSidebar() {
      this.sidebarCollapsed = !this.sidebarCollapsed
    },
    setMapProvider(provider) {
      this.mapProvider = provider
    },
  },
})