import { createI18n } from 'vue-i18n'

const messages = {}

const zhModules = import.meta.glob('./zh-CN/*.json', { eager: true })
for (const path in zhModules) {
  const key = path.replace('./zh-CN/', '').replace('.json', '')
  messages['zh-CN'] = messages['zh-CN'] || {}
  messages['zh-CN'][key] = zhModules[path].default || zhModules[path]
}

const enModules = import.meta.glob('./en-US/*.json', { eager: true })
for (const path in enModules) {
  const key = path.replace('./en-US/', '').replace('.json', '')
  messages['en-US'] = messages['en-US'] || {}
  messages['en-US'][key] = enModules[path].default || enModules[path]
}

const savedLocale = localStorage.getItem('jte-locale')
const browserLocale = navigator.language || 'zh-CN'
const defaultLocale = savedLocale || (browserLocale.startsWith('zh') ? 'zh-CN' : 'en-US')

const i18n = createI18n({
  legacy: false,
  locale: defaultLocale,
  fallbackLocale: 'zh-CN',
  messages,
})

export default i18n