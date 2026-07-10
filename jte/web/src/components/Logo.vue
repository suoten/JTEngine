<template>
  <div class="jte-logo" :class="{ 'collapsed': collapsed }">
    <svg v-if="variant === 'icon'" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200" :width="size" :height="size">
      <defs>
        <linearGradient id="logoGrad1" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" style="stop-color:#3b82f6;stop-opacity:1" />
          <stop offset="50%" style="stop-color:#6366f1;stop-opacity:1" />
          <stop offset="100%" style="stop-color:#8b5cf6;stop-opacity:1" />
        </linearGradient>
        <linearGradient id="logoGrad2" x1="0%" y1="0%" x2="100%" y2="100%">
          <stop offset="0%" style="stop-color:#60a5fa;stop-opacity:1" />
          <stop offset="100%" style="stop-color:#a78bfa;stop-opacity:1" />
        </linearGradient>
      </defs>
      <polygon points="100,25 164.54,62.5 164.54,137.5 100,175 35.46,137.5 35.46,62.5" 
               fill="url(#logoGrad1)" opacity="0.15"/>
      <polygon points="100,15 173.2,57.5 173.2,142.5 100,185 26.8,142.5 26.8,57.5" 
               fill="none" stroke="url(#logoGrad1)" stroke-width="2" opacity="0.4"/>
      <path d="M70 55 L70 115 Q70 145 100 145 Q130 145 130 115 L130 95" 
            fill="none" stroke="url(#logoGrad1)" stroke-width="8" stroke-linecap="round"/>
      <path d="M130 95 L150 95" 
            fill="none" :stroke="dark ? '#fff' : 'url(#logoGrad2)'" stroke-width="6" stroke-linecap="round"/>
      <circle cx="100" cy="55" r="8" fill="url(#logoGrad2)"/>
      <circle cx="100" cy="145" r="10" fill="none" stroke="url(#logoGrad2)" stroke-width="2" opacity="0.8"/>
      <circle cx="100" cy="145" r="4" fill="url(#logoGrad2)"/>
    </svg>
    <div v-else class="logo-full">
      <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 80 80" :width="iconSize" :height="iconSize">
        <defs>
          <linearGradient id="logoGrad3" x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" style="stop-color:#3b82f6;stop-opacity:1" />
            <stop offset="50%" style="stop-color:#6366f1;stop-opacity:1" />
            <stop offset="100%" style="stop-color:#8b5cf6;stop-opacity:1" />
          </linearGradient>
          <linearGradient id="logoGrad4" x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" style="stop-color:#60a5fa;stop-opacity:1" />
            <stop offset="100%" style="stop-color:#a78bfa;stop-opacity:1" />
          </linearGradient>
        </defs>
        <polygon points="40,6 69.28,23 69.28,57 40,74 10.72,57 10.72,23" 
                 fill="url(#logoGrad3)" opacity="0.15"/>
        <polygon points="40,10 65.82,25 65.82,55 40,70 14.18,55 14.18,25" 
                 fill="none" stroke="url(#logoGrad3)" stroke-width="1.5" opacity="0.4"/>
        <path d="M28 22 L28 46 Q28 58 40 58 Q52 58 52 46 L52 38" 
              fill="none" stroke="url(#logoGrad3)" stroke-width="3.5" stroke-linecap="round"/>
        <path d="M52 38 L60 38" 
              fill="none" :stroke="dark ? '#fff' : 'url(#logoGrad4)'" stroke-width="3" stroke-linecap="round"/>
        <circle cx="40" cy="22" r="3.5" fill="url(#logoGrad4)"/>
        <circle cx="40" cy="58" r="4" fill="none" stroke="url(#logoGrad4)" stroke-width="1" opacity="0.8"/>
        <circle cx="40" cy="58" r="1.8" fill="url(#logoGrad4)"/>
      </svg>
      <div class="logo-text" v-if="!collapsed">
        <span class="logo-name" :style="{ color: dark ? '#fff' : undefined }">JTE</span>
        <span class="logo-sub" v-if="showSub">{{ subtitle }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
defineProps({
  size: { type: Number, default: 40 },
  iconSize: { type: Number, default: 36 },
  variant: { type: String, default: 'full' },
  collapsed: { type: Boolean, default: false },
  dark: { type: Boolean, default: false },
  showSub: { type: Boolean, default: true },
  subtitle: { type: String, default: 'JT Engine' }
})
</script>

<style scoped>
.jte-logo { display: flex; align-items: center; }
.logo-full { display: flex; align-items: center; gap: 10px; }
.logo-text { display: flex; flex-direction: column; line-height: 1.1; }
.logo-name {
  font-size: 20px;
  font-weight: 800;
  letter-spacing: -0.02em;
  background: linear-gradient(135deg, #3b82f6, #6366f1, #8b5cf6);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}
.logo-sub {
  font-size: 11px;
  font-weight: 500;
  color: var(--jte-text-muted, #64748b);
  letter-spacing: 0.05em;
}
</style>
