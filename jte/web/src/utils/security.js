// ============================================================================
// 安全工具集（等保2.0 + 防克隆加固）
//
// 功能：
//   1. 设备指纹采集（Canvas/WebGL/字体/UA/屏幕/时区）—— 登录时上报后端绑定校验
//   2. XSS 防护：HTML 转义 + DOMPurify 风格的标签白名单清理
//   3. 敏感数据脱敏：手机号/身份证/车牌/邮箱/姓名（前端兜底，后端为主）
//   4. CSRF token 管理
// ============================================================================

// ---------------------------------------------------------------------------
// 1. 设备指纹采集（防克隆：鉴权码绑定手机号 + 设备指纹）
// ---------------------------------------------------------------------------

/**
 * 采集设备指纹特征并生成稳定哈希。
 * 指纹由浏览器环境特征组合而成，同一设备/浏览器下保持稳定。
 * 后端收到后用 SM3 摘要生成统一指纹，用于：
 *   - 新设备登录告警
 *   - 异常设备检测
 *   - 鉴权码绑定校验
 */
export function collectDeviceFingerprint() {
  const features = {
    user_agent: navigator.userAgent || '',
    accept_language: navigator.language || (navigator.languages || []).join(','),
    accept_encoding: '', // 浏览器不暴露，留空
    screen: getScreenInfo(),
    timezone: getTimezone(),
    platform: navigator.platform || '',
    canvas_hash: getCanvasHash(),
    webgl_hash: getWebGLHash(),
    fonts_hash: getFontsHash(),
  }
  return features
}

function getScreenInfo() {
  try {
    return `${window.screen.width}x${window.screen.height}x${window.screen.colorDepth}`
  } catch {
    return ''
  }
}

function getTimezone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

/**
 * Canvas 指纹：绘制特定文本+图形后取 toDataURL 的哈希。
 * 不同设备/浏览器因 GPU、字体渲染、抗锯齿差异产生不同结果。
 */
function getCanvasHash() {
  try {
    const canvas = document.createElement('canvas')
    canvas.width = 220
    canvas.height = 30
    const ctx = canvas.getContext('2d')
    if (!ctx) return ''
    ctx.textBaseline = 'top'
    ctx.font = '14px Arial'
    ctx.fillStyle = '#f60'
    ctx.fillRect(0, 0, 100, 20)
    ctx.fillStyle = '#069'
    ctx.fillText('JTE-fingerprint-🚀', 2, 2)
    ctx.fillStyle = 'rgba(102, 204, 0, 0.7)'
    ctx.fillText('JTE-fingerprint-🚀', 4, 4)
    const dataUrl = canvas.toDataURL()
    return hashString(dataUrl)
  } catch {
    return ''
  }
}

/**
 * WebGL 指纹：读取 GPU 厂商/渲染器信息后哈希。
 */
function getWebGLHash() {
  try {
    const canvas = document.createElement('canvas')
    const gl = canvas.getContext('webgl') || canvas.getContext('experimental-webgl')
    if (!gl) return ''
    const debugInfo = gl.getExtension('WEBGL_debug_renderer_info')
    if (!debugInfo) return ''
    const vendor = gl.getParameter(debugInfo.UNMASKED_VENDOR_WEBGL) || ''
    const renderer = gl.getParameter(debugInfo.UNMASKED_RENDERER_WEBGL) || ''
    return hashString(vendor + '|' + renderer)
  } catch {
    return ''
  }
}

/**
 * 字体指纹：探测系统是否安装常见字体，组合后哈希。
 */
function getFontsHash() {
  try {
    const testFonts = [
      'Arial', 'Helvetica', 'Times New Roman', 'Courier New', 'Verdana',
      'Georgia', 'Palatino', 'Garamond', 'Comic Sans MS', 'Trebuchet MS',
      'Microsoft YaHei', 'SimSun', 'SimHei', 'KaiTi', 'FangSong',
    ]
    const baseFonts = ['monospace', 'sans-serif', 'serif']
    const testString = 'mmmmmmmmmmlli'
    const testSize = '72px'

    const span = document.createElement('span')
    span.style.fontSize = testSize
    span.style.position = 'absolute'
    span.style.left = '-9999px'
    span.style.visibility = 'hidden'
    span.textContent = testString
    document.body.appendChild(span)

    const defaultWidth = {}
    const defaultHeight = {}
    for (const base of baseFonts) {
      span.style.fontFamily = base
      defaultWidth[base] = span.offsetWidth
      defaultHeight[base] = span.offsetHeight
    }

    const detected = []
    for (const font of testFonts) {
      let isDetected = false
      for (const base of baseFonts) {
        span.style.fontFamily = `"${font}", ${base}`
        if (span.offsetWidth !== defaultWidth[base] || span.offsetHeight !== defaultHeight[base]) {
          isDetected = true
          break
        }
      }
      if (isDetected) detected.push(font)
    }
    document.body.removeChild(span)
    return hashString(detected.join(','))
  } catch {
    return ''
  }
}

/**
 * 简单字符串哈希（FNV-1a 变体）—— 仅用于客户端指纹生成，
 * 最终指纹由后端 SM3 摘要生成（国密合规）。
 */
function hashString(str) {
  if (!str) return ''
  let hash = 0x811c9dc5
  for (let i = 0; i < str.length; i++) {
    hash ^= str.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193)
  }
  // 转 hex（32 位无符号）
  return (hash >>> 0).toString(16).padStart(8, '0')
}

// ---------------------------------------------------------------------------
// 2. XSS 防护（前端输入清理 + 输出转义）
// ---------------------------------------------------------------------------

const HTML_ESCAPE_MAP = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
  '/': '&#x2F;',
  '`': '&#x60;',
  '=': '&#x3D;',
}

/**
 * HTML 转义：将用户输入转为安全文本，防止 XSS 注入。
 * 用于 v-html 绑定前的清理，或动态拼接 HTML 时的转义。
 *
 * @param {string} str 原始字符串
 * @returns {string} 转义后的字符串
 */
export function escapeHtml(str) {
  if (str == null) return ''
  return String(str).replace(/[&<>"'`/=]/g, (ch) => HTML_ESCAPE_MAP[ch])
}

/**
 * 反转义 HTML（用于从后端获取的已转义文本展示）
 */
export function unescapeHtml(str) {
  if (!str) return ''
  const entries = Object.entries(HTML_ESCAPE_MAP)
  let result = String(str)
  for (const [char, entity] of entries) {
    result = result.replace(new RegExp(entity, 'g'), char)
  }
  return result
}

// 允许的 HTML 标签白名单（用于 sanitizeHtml）
const ALLOWED_TAGS = new Set([
  'p', 'br', 'hr', 'span', 'div', 'strong', 'em', 'b', 'i', 'u', 's',
  'ul', 'ol', 'li', 'blockquote', 'code', 'pre', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
  'a', 'img', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
])

// 允许的 HTML 属性白名单
const ALLOWED_ATTRS = new Set([
  'href', 'src', 'alt', 'title', 'class', 'id', 'width', 'height',
  'colspan', 'rowspan', 'target', 'rel',
])

// 危险属性前缀（事件处理器/on* 一律移除）
const DANGEROUS_ATTR_PREFIX = 'on'

/**
 * HTML 清理：移除危险标签和属性，保留白名单内的安全元素。
 * 用于用户输入富文本的场景（如告警备注、报表描述）。
 *
 * 简化版 DOMPurify 实现：不依赖第三方库，基于 DOMParser 解析后白名单过滤。
 * 生产环境对不可信内容建议额外引入 DOMPurify 做二次清理。
 *
 * @param {string} html 原始 HTML
 * @param {object} options { allowedTags?, allowedAttrs? }
 * @returns {string} 清理后的安全 HTML
 */
export function sanitizeHtml(html, options = {}) {
  if (!html) return ''
  const allowedTags = options.allowedTags || ALLOWED_TAGS
  const allowedAttrs = options.allowedAttrs || ALLOWED_ATTRS

  try {
    const doc = new DOMParser().parseFromString(html, 'text/html')
    const result = []
    doc.body.childNodes.forEach((node) => {
      result.push(sanitizeNode(node, allowedTags, allowedAttrs))
    })
    return result.join('')
  } catch {
    // DOMParser 不可用（SSR 环境）时降级为全转义
    return escapeHtml(html)
  }
}

function sanitizeNode(node, allowedTags, allowedAttrs) {
  if (node.nodeType === Node.TEXT_NODE) {
    return escapeHtml(node.textContent)
  }
  if (node.nodeType !== Node.ELEMENT_NODE) {
    return ''
  }
  const tag = node.tagName.toLowerCase()
  if (!allowedTags.has(tag)) {
    // 不在白名单的标签：递归处理子节点（保留文本内容）
    const children = []
    node.childNodes.forEach((child) => {
      children.push(sanitizeNode(child, allowedTags, allowedAttrs))
    })
    return children.join('')
  }

  // 过滤属性
  const safeAttrs = []
  for (const attr of node.attributes) {
    const name = attr.name.toLowerCase()
    const value = attr.value
    // 移除事件处理器和危险属性
    if (name.startsWith(DANGEROUS_ATTR_PREFIX)) continue
    if (!allowedAttrs.has(name)) continue
    // href/src 不能是 javascript: 协议
    if ((name === 'href' || name === 'src') && /^\s*javascript:/i.test(value)) continue
    safeAttrs.push(`${name}="${escapeAttr(value)}"`)
  }

  const attrStr = safeAttrs.length > 0 ? ' ' + safeAttrs.join(' ') : ''
  const children = []
  node.childNodes.forEach((child) => {
    children.push(sanitizeNode(child, allowedTags, allowedAttrs))
  })
  return `<${tag}${attrStr}>${children.join('')}</${tag}>`
}

function escapeAttr(value) {
  return String(value).replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// ---------------------------------------------------------------------------
// 3. 敏感数据脱敏（前端兜底，后端为主要责任方）
// ---------------------------------------------------------------------------

/**
 * 手机号脱敏：138****8000
 */
export function maskPhone(phone) {
  if (!phone || phone.length < 7) return phone
  return phone.replace(/(\d{3})\d{4}(\d+)/, '$1****$2')
}

/**
 * 身份证号脱敏：110101********1234
 */
export function maskIDCard(idCard) {
  if (!idCard || idCard.length < 10) return idCard
  const len = idCard.length
  const prefix = idCard.substring(0, 6)
  const suffix = idCard.substring(len - 4)
  return prefix + '*'.repeat(len - 10) + suffix
}

/**
 * 车牌号脱敏：京A***45
 */
export function maskPlate(plate) {
  if (!plate || plate.length < 5) return plate
  const prefix = plate.substring(0, 2)
  const suffix = plate.substring(plate.length - 2)
  return prefix + '*'.repeat(plate.length - 4) + suffix
}

/**
 * 邮箱脱敏：z***@example.com
 */
export function maskEmail(email) {
  if (!email || !email.includes('@')) return email
  const [name, domain] = email.split('@')
  if (name.length <= 1) return '*@' + domain
  return name[0] + '*'.repeat(Math.min(name.length - 1, 3)) + '@' + domain
}

/**
 * 姓名脱敏：张*丰
 */
export function maskName(name) {
  if (!name) return name
  if (name.length <= 1) return name
  if (name.length === 2) return name[0] + '*'
  return name[0] + '*'.repeat(name.length - 2) + name[name.length - 1]
}

/**
 * 按字段名自动识别并脱敏
 * @param {string} field 字段名
 * @param {string} value 字段值
 */
export function maskByField(field, value) {
  if (!value) return value
  const lower = String(field).toLowerCase()
  if (isPhoneField(lower)) return maskPhone(value)
  if (isIDCardField(lower)) return maskIDCard(value)
  if (isPlateField(lower)) return maskPlate(value)
  if (isEmailField(lower)) return maskEmail(value)
  if (isNameField(lower)) return maskName(value)
  return value
}

function isPhoneField(field) {
  return field === 'phone' || field === 'mobile' || field === 'tel'
    || field.includes('phone') || field.includes('mobile')
}
function isIDCardField(field) {
  return field.includes('id_card') || field.includes('idcard') || field.includes('id_number')
    || field === 'identity' || field.includes('identity')
}
function isPlateField(field) {
  return field === 'plate' || field === 'plate_no' || field === 'license'
    || field === 'car_no' || field.includes('plate')
}
function isEmailField(field) {
  return field === 'email' || field === 'mail' || field.includes('email')
}
function isNameField(field) {
  return field === 'name' || field === 'driver_name' || field === 'owner_name'
    || field === 'contact_name' || field.endsWith('_name')
}

// ---------------------------------------------------------------------------
// 4. CSRF token 管理
// ---------------------------------------------------------------------------

let csrfToken = ''

/**
 * 设置 CSRF token（从登录响应或 meta 标签获取）
 */
export function setCSRFToken(token) {
  csrfToken = token || ''
  if (token) {
    // 同步到 meta 标签供 axios 拦截器读取
    const meta = document.querySelector('meta[name="csrf-token"]')
    if (meta) {
      meta.setAttribute('content', token)
    } else {
      const newMeta = document.createElement('meta')
      newMeta.name = 'csrf-token'
      newMeta.content = token
      document.head.appendChild(newMeta)
    }
  }
}

export function getCSRFToken() {
  if (csrfToken) return csrfToken
  const meta = document.querySelector('meta[name="csrf-token"]')
  return meta ? meta.getAttribute('content') : ''
}

export default {
  collectDeviceFingerprint,
  escapeHtml,
  unescapeHtml,
  sanitizeHtml,
  maskPhone,
  maskIDCard,
  maskPlate,
  maskEmail,
  maskName,
  maskByField,
  setCSRFToken,
  getCSRFToken,
}
