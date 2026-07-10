// 安全工具集单元测试
// 覆盖：XSS 转义/清理、敏感数据脱敏、设备指纹、CSRF token 管理
import { describe, it, expect, beforeEach, vi } from 'vitest'

// Mock 浏览器环境（vitest 默认 jsdom 已提供 document/window）
import {
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
  collectDeviceFingerprint,
} from './security'

describe('XSS 防护', () => {
  describe('escapeHtml', () => {
    it('应转义 HTML 特殊字符（含 / 防止 </script> 攻击）', () => {
      expect(escapeHtml('<script>alert("xss")</script>'))
        .toBe('&lt;script&gt;alert(&quot;xss&quot;)&lt;&#x2F;script&gt;')
    })

    it('应转义单引号和反引号', () => {
      expect(escapeHtml("'`")).toBe('&#39;&#x60;')
    })

    it('应处理 null/undefined 输入', () => {
      expect(escapeHtml(null)).toBe('')
      expect(escapeHtml(undefined)).toBe('')
    })

    it('应处理非字符串输入（转为字符串）', () => {
      expect(escapeHtml(123)).toBe('123')
    })
  })

  describe('unescapeHtml', () => {
    it('应反转义 HTML 实体', () => {
      const escaped = escapeHtml('<div class="x">test</div>')
      expect(unescapeHtml(escaped)).toBe('<div class="x">test</div>')
    })

    it('应处理空输入', () => {
      expect(unescapeHtml('')).toBe('')
      expect(unescapeHtml(null)).toBe('')
    })
  })

  describe('sanitizeHtml', () => {
    it('应移除 script 标签', () => {
      const result = sanitizeHtml('<script>alert("xss")</script><p>safe</p>')
      expect(result).not.toContain('<script>')
      expect(result).toContain('<p>safe</p>')
    })

    it('应移除事件处理器属性', () => {
      const result = sanitizeHtml('<p onclick="alert(1)">text</p>')
      expect(result).not.toContain('onclick')
      expect(result).toContain('<p>text</p>')
    })

    it('应移除 javascript: 协议', () => {
      const result = sanitizeHtml('<a href="javascript:alert(1)">link</a>')
      expect(result).not.toContain('javascript:')
    })

    it('应保留白名单内的安全标签', () => {
      const result = sanitizeHtml('<p>段落</p><strong>加粗</strong><em>斜体</em>')
      expect(result).toContain('<p>段落</p>')
      expect(result).toContain('<strong>加粗</strong>')
      expect(result).toContain('<em>斜体</em>')
    })

    it('应保留安全属性', () => {
      const result = sanitizeHtml('<a href="https://example.com" target="_blank">链接</a>')
      expect(result).toContain('href="https://example.com"')
      expect(result).toContain('target="_blank"')
    })

    it('应处理空输入', () => {
      expect(sanitizeHtml('')).toBe('')
      expect(sanitizeHtml(null)).toBe('')
    })

    it('应递归处理嵌套标签', () => {
      const result = sanitizeHtml('<div><p>text</p></div>')
      expect(result).toContain('<div><p>text</p></div>')
    })
  })
})

describe('敏感数据脱敏', () => {
  describe('maskPhone', () => {
    it('应脱敏手机号中间 4 位', () => {
      expect(maskPhone('13812345678')).toBe('138****5678')
    })

    it('短号码不脱敏', () => {
      expect(maskPhone('12345')).toBe('12345')
    })

    it('应处理空输入', () => {
      expect(maskPhone('')).toBe('')
      expect(maskPhone(null)).toBe(null)
    })
  })

  describe('maskIDCard', () => {
    it('应脱敏身份证号中间部分', () => {
      expect(maskIDCard('110101199001011234')).toBe('110101********1234')
    })

    it('应支持 18 位带 X 结尾的身份证', () => {
      const result = maskIDCard('11010119900101123X')
      expect(result).toMatch(/^110101\*+123X$/)
    })

    it('短号码不脱敏', () => {
      expect(maskIDCard('12345')).toBe('12345')
    })
  })

  describe('maskPlate', () => {
    it('应脱敏车牌号中间部分', () => {
      const result = maskPlate('京A12345')
      expect(result).toMatch(/^京A\*+45$/)
    })

    it('应处理空输入', () => {
      expect(maskPlate('')).toBe('')
    })
  })

  describe('maskEmail', () => {
    it('应脱敏邮箱用户名部分', () => {
      const result = maskEmail('user@example.com')
      expect(result).toMatch(/^u\*+@example\.com$/)
    })

    it('单字符用户名应全部脱敏', () => {
      expect(maskEmail('a@example.com')).toBe('*@example.com')
    })

    it('无 @ 的字符串不脱敏', () => {
      expect(maskEmail('notanemail')).toBe('notanemail')
    })
  })

  describe('maskName', () => {
    it('两字姓名脱敏第二字', () => {
      expect(maskName('张三')).toBe('张*')
    })

    it('三字及以上脱敏中间', () => {
      expect(maskName('张三丰')).toBe('张*丰')
      expect(maskName('诸葛亮亮')).toBe('诸**亮')
    })

    it('单字姓名不脱敏', () => {
      expect(maskName('张')).toBe('张')
    })
  })

  describe('maskByField', () => {
    it('应按字段名识别手机号', () => {
      expect(maskByField('phone', '13812345678')).toBe('138****5678')
      expect(maskByField('mobile', '13812345678')).toBe('138****5678')
    })

    it('应按字段名识别身份证', () => {
      const result = maskByField('id_card', '110101199001011234')
      expect(result).toContain('*')
      expect(result).toBe('110101********1234')
    })

    it('应按字段名识别车牌', () => {
      const result = maskByField('plate_no', '京A12345')
      expect(result).toContain('*')
    })

    it('应按字段名识别邮箱', () => {
      const result = maskByField('email', 'user@example.com')
      expect(result).toContain('*')
      expect(result).toContain('@example.com')
    })

    it('应按字段名识别姓名', () => {
      expect(maskByField('driver_name', '张三丰')).toBe('张*丰')
      expect(maskByField('owner_name', '李四')).toBe('李*')
    })

    it('非敏感字段不脱敏', () => {
      expect(maskByField('address', '北京市朝阳区')).toBe('北京市朝阳区')
    })
  })
})

describe('CSRF token 管理', () => {
  beforeEach(() => {
    // 清理 meta 标签和模块内缓存
    document.querySelectorAll('meta[name="csrf-token"]').forEach(m => m.remove())
    setCSRFToken('')
  })

  it('应设置并获取 CSRF token', () => {
    setCSRFToken('test-csrf-token-123')
    expect(getCSRFToken()).toBe('test-csrf-token-123')
  })

  it('应在 meta 标签中同步 CSRF token', () => {
    setCSRFToken('meta-token-456')
    const meta = document.querySelector('meta[name="csrf-token"]')
    expect(meta).not.toBeNull()
    expect(meta.getAttribute('content')).toBe('meta-token-456')
  })

  it('空 token 不应设置 meta 标签', () => {
    setCSRFToken('')
    const meta = document.querySelector('meta[name="csrf-token"]')
    expect(meta).toBeNull()
  })
})

describe('设备指纹采集', () => {
  it('应返回完整的指纹特征对象', () => {
    // jsdom 不实现 canvas.getContext，但代码有 try/catch 兜底返回空字符串
    const fp = collectDeviceFingerprint()
    expect(fp).toHaveProperty('user_agent')
    expect(fp).toHaveProperty('screen')
    expect(fp).toHaveProperty('timezone')
    expect(fp).toHaveProperty('platform')
    expect(fp).toHaveProperty('canvas_hash')
    expect(fp).toHaveProperty('webgl_hash')
    expect(fp).toHaveProperty('fonts_hash')
    // jsdom 环境下 canvas/webgl 可能返回空字符串，但字段必须存在
    expect(typeof fp.canvas_hash).toBe('string')
    expect(typeof fp.webgl_hash).toBe('string')
  })

  it('同一环境下多次采集应产生稳定指纹', () => {
    const fp1 = collectDeviceFingerprint()
    const fp2 = collectDeviceFingerprint()
    // UA/屏幕/时区/平台在同一环境下应稳定
    expect(fp1.user_agent).toBe(fp2.user_agent)
    expect(fp1.screen).toBe(fp2.screen)
    expect(fp1.timezone).toBe(fp2.timezone)
    expect(fp1.platform).toBe(fp2.platform)
  })
})
