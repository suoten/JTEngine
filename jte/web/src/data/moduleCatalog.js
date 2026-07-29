// AUTO-FIX-2026-06-30 [P1-7]: 可授权模块目录（功能 + 价格 + 说明）
// 供 ModulePurchaseModal 与 Auth.vue 共享。价格为展示用，实际购买跳转官网。
// 与后端 TrialModules / License.Modules 名称对齐。
export const MODULE_CATALOG = [
  {
    name: 'protocol_809',
    label: 'JT/T 809 平台级联',
    description: '上下级平台级联，支持位置/报警/车辆数据双向转发，主从链路独立重连。',
    features: ['主从链路独立指数退避重连', '按 SN 顺序断线补发', '转发规则持久化与热更新', '上级平台数据过滤'],
    price: '¥ 12,000',
    trialDays: 30,
  },
  {
    name: 'protocol_1045',
    label: 'JT/T 1045 主动安全',
    description: 'DSM/ADAS 主动安全报警接入，AI 误报过滤，报警闭环处理。',
    features: ['DSM 驾驶行为报警解析', 'ADAS 前向碰撞报警', 'AI 误报过滤链路', '报警置信度回写'],
    price: '¥ 9,800',
    trialDays: 30,
  },
  {
    name: 'protocol_1078',
    label: 'JT/T 1078 音视频',
    description: '音视频实时回传与历史回放，RTP over UDP/TCP，RFC 4571 封装。',
    features: ['WebRTC/FLV/HLS 多协议播放', 'RTP over TCP 自动 fallback', '视频质量实时统计', '关键帧手动触发', '16 画面分屏'],
    price: '¥ 15,000',
    trialDays: 30,
  },
  {
    name: 'protocol_905',
    label: 'JT/T 905 北斗',
    description: '北斗短报文通信与位置服务接入。',
    features: ['短报文收发', '北斗位置解析', '双协议融合定位'],
    price: '¥ 6,000',
    trialDays: 30,
  },
  {
    name: 'storage',
    label: '数据存储与报表',
    description: '关系库/时序库/缓存/对象存储分层管理，TTL 配置与冷数据归档。',
    features: ['TDengine 千万点/秒写入', '冷热分层自动归档', 'TTL 保留期配置', '报表中心（日/周/月报）', '缓存命中率监控'],
    price: '¥ 18,000',
    trialDays: 30,
  },
  {
    name: 'ai',
    label: 'AI 智能分析',
    description: '报警误报过滤、疲劳驾驶识别、风险评分，多模型推理引擎。',
    features: ['报警 AI 过滤', '疲劳驾驶检测', '风险评分模型', '推理引擎加速'],
    price: '¥ 20,000',
    trialDays: 30,
  },
  {
    name: 'ai_nlp',
    label: 'AI 对话助手',
    description: '自然语言查询、NL2SQL、协议调试助手、知识库 RAG 检索。',
    features: ['NL2SQL 智能查询', '协议调试助手', 'RAG 知识库检索', '报表自动生成'],
    price: '¥ 14,000',
    trialDays: 30,
  },
]

// 按模块名查找目录项
export function findModule(moduleName) {
  return MODULE_CATALOG.find(m => m.name === moduleName || m.aliases?.includes(moduleName))
}
