import { defineStore } from 'pinia'
import { usePermissionStore } from './permission'

// AUTO-FIX-2026-07-02: 数据权限集中管理 Store
// 基础实现：按组织/车辆/设备的数据范围过滤
//
// 数据范围类型（DataScope.scope_type）：
//   - all: 全部数据（super_admin/admin 默认）
//   - org: 按组织过滤（附加 org_id 条件）
//   - vehicle: 按指定车辆过滤（附加 vehicle_ids IN (...) 条件）
//   - self: 仅自己创建的数据（附加 created_by 条件）
//
// 工作原理：
//   1. 登录后 /auth/permissions 接口返回 data_scope，permission store 缓存
//   2. api/index.js 请求拦截器自动从 localStorage 读取并附加到 GET 请求参数
//   3. 本 store 提供集中化的数据权限查询/应用方法，供组件层使用
//
// 注意：后端 middleware/auth.go 的 ApplyDataScopeToParams 是权威过滤源，
//      前端附加条件仅为 UI 体验优化（避免显示无权查看的数据），
//      后端仍会强制过滤确保安全。

export const useDataPermStore = defineStore('dataperm', {
  state: () => ({
    // 数据范围（与 permission store.dataScope 同步）
    // { scope_type, org_id, vehicle_ids }
  }),

  getters: {
    // 当前数据范围（从 permission store 派生，保持单一数据源）
    dataScope: () => {
      const permStore = usePermissionStore()
      return permStore.dataScope || { scope_type: 'all', org_id: '', vehicle_ids: [] }
    },

    // 范围类型
    scopeType: () => {
      const permStore = usePermissionStore()
      return permStore.dataScope?.scope_type || 'all'
    },

    // 是否拥有全部数据权限
    isAllScope: () => {
      const permStore = usePermissionStore()
      return (permStore.dataScope?.scope_type || 'all') === 'all'
    },

    // 是否按组织过滤
    isOrgScope: () => {
      const permStore = usePermissionStore()
      return permStore.dataScope?.scope_type === 'org'
    },

    // 是否按车辆过滤
    isVehicleScope: () => {
      const permStore = usePermissionStore()
      return permStore.dataScope?.scope_type === 'vehicle'
    },

    // 是否仅自己
    isSelfScope: () => {
      const permStore = usePermissionStore()
      return permStore.dataScope?.scope_type === 'self'
    },

    // 组织 ID
    orgId: () => {
      const permStore = usePermissionStore()
      return permStore.dataScope?.org_id || ''
    },

    // 车辆 ID 列表
    vehicleIds: () => {
      const permStore = usePermissionStore()
      return permStore.dataScope?.vehicle_ids || []
    },

    // 数据范围中文描述（供 UI 显示）
    scopeLabel: () => {
      const permStore = usePermissionStore()
      const st = permStore.dataScope?.scope_type || 'all'
      const labels = {
        all: '全部数据',
        org: '本组织数据',
        vehicle: '指定车辆数据',
        self: '仅自己的数据',
      }
      return labels[st] || st
    },
  },

  actions: {
    // 将数据范围应用到查询参数 map
    // 组件层调用此方法构建带数据权限过滤的查询条件
    // 注意：api/index.js 拦截器已自动附加，此方法供手动构建查询时使用
    applyToParams(params = {}) {
      const permStore = usePermissionStore()
      const ds = permStore.dataScope
      if (!ds) return params

      switch (ds.scope_type) {
        case 'all':
          // 不附加任何过滤
          break
        case 'org':
          if (ds.org_id && !params.org_id) {
            params.org_id = ds.org_id
          }
          break
        case 'vehicle':
          if (ds.vehicle_ids && ds.vehicle_ids.length > 0 && !params.vehicle_ids) {
            params.vehicle_ids = ds.vehicle_ids.join(',')
          }
          break
        case 'self':
          if (permStore.currentUser && !params.created_by) {
            params.created_by = permStore.currentUser.id
          }
          break
      }
      return params
    },

    // 检查是否可查看指定车辆（vehicle 范围下仅能查看授权车辆）
    canViewVehicle(vehicleId) {
      const permStore = usePermissionStore()
      const ds = permStore.dataScope
      if (!ds) return true
      if (ds.scope_type === 'all') return true
      if (ds.scope_type === 'vehicle') {
        return ds.vehicle_ids?.includes(vehicleId) ?? false
      }
      // org/self 范围下由后端按 org_id/created_by 过滤，前端不预判
      return true
    },

    // 检查是否可查看指定组织
    canViewOrg(orgId) {
      const permStore = usePermissionStore()
      const ds = permStore.dataScope
      if (!ds) return true
      if (ds.scope_type === 'all') return true
      if (ds.scope_type === 'org') {
        return ds.org_id === orgId
      }
      return true
    },
  },
})
