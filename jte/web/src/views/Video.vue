<template>
  <div>
    <div class="page-header">
      <div style="display: flex; align-items: center; justify-content: space-between;">
        <div>
          <h1 class="page-title">{{ $t('common.nav.video') }}</h1>
          <p style="color: var(--jte-text-muted); font-size: 13px; margin-top: 4px;">
            JT/T 1078 实时音视频监控，支持FLV/HLS/WebRTC
          </p>
        </div>
        <div style="display: flex; align-items: center; gap: 12px;">
          <el-select v-model="layoutMode" size="small" style="width: 100px;">
            <el-option label="1画面" :value="1" />
            <el-option label="4画面" :value="4" />
            <el-option label="9画面" :value="9" />
            <el-option label="16画面" :value="16" />
          </el-select>
          <el-select v-model="streamSchema" size="small" style="width: 120px;">
            <el-option label="FLV" value="flv" />
            <el-option label="HLS" value="hls" />
            <el-option label="WebRTC" value="webrtc" />
          </el-select>
          <el-tooltip content="网络质量下降时自动切换到子码流" placement="bottom">
            <el-switch v-model="autoAdaptive" size="small" active-text="自适应" />
          </el-tooltip>
          <el-button size="small" @click="showPlaybackDialog = true">回放</el-button>
          <el-button size="small" @click="showDownloadDialog = true">下载</el-button>
          <el-button type="primary" size="small" @click="showStartDialog = true">发起实时视频</el-button>
        </div>
      </div>
    </div>

    <div class="page-content">
      <!--
        AUTO-FIX-2026-06-29 [P1-5]: VideoGrid 分屏容器
        16 画面模式下非激活画面显示占位，不渲染 video 元素，不拉流
      -->
      <VideoGrid
        :streams="streams"
        :layout-mode="layoutMode"
        :active-id="activeStreamId"
        @activate="handleActivate"
      >
        <template #default="{ stream }">
          <el-card shadow="never">
            <div class="video-container">
              <video
                v-if="stream.url && streamSchema === 'flv'"
                :id="`video-${stream.id}`"
                controls
                autoplay
                muted
                style="width: 100%; height: 100%; object-fit: contain;"
              />
              <video
                v-else-if="stream.url && streamSchema === 'hls'"
                :id="`video-${stream.id}`"
                :src="stream.hls_url || stream.url"
                controls
                autoplay
                muted
                style="width: 100%; height: 100%; object-fit: contain;"
              />
              <video
                v-else-if="stream.url && streamSchema === 'webrtc'"
                :id="`video-${stream.id}`"
                autoplay
                muted
                playsinline
                style="width: 100%; height: 100%; object-fit: contain;"
              />
              <div v-else class="video-placeholder">
                <el-icon :size="48" color="var(--jte-text-muted)"><VideoCamera /></el-icon>
                <p style="margin-top: 8px; color: var(--jte-text-muted); font-size: 12px;">等待视频流...</p>
              </div>
              <!-- AUTO-FIX-2026-06-29 [P0-2]: 悬浮可收起质量面板（本地+后端双数据源） -->
              <QualityPanel
                :local-stats="qualityStats[stream.id] || {}"
                :vehicle-id="stream.vehicle_id"
                :channel="stream.channel"
                :status="stream.status"
              />
              <div v-if="bufferStates[stream.id]" class="buffer-overlay">
                <el-icon class="buffer-spinner"><Loading /></el-icon>
                <span>{{ stream.status === 'reconnecting' ? '网络异常，正在重连...' : '缓冲中...' }}</span>
              </div>
            </div>
            <!-- AUTO-FIX-2026-06-29 [P0-3]: VideoToolbar 控件栏（含关键帧 loading） -->
            <VideoToolbar
              :stream="stream"
              v-model:ptz-speed="ptzSpeed"
              :local-stats="qualityStats[stream.id] || null"
              @switch-channel="(val) => switchChannel(stream, val)"
              @switch-stream-type="(val) => switchStreamType(stream, val)"
              @ptz-control="(dir) => ptzControl(stream, dir)"
              @screenshot="screenshot(stream)"
              @toggle-stream-mode="toggleStreamMode(stream)"
              @toggle-fullscreen="toggleFullscreen(stream.id)"
              @stop="stopStream(stream)"
            />
            <div v-if="stream.error" style="color: #ef4444; font-size: 11px; margin-top: 4px;">{{ stream.error }}</div>
          </el-card>
        </template>
      </VideoGrid>

      <el-empty v-if="streams.length === 0" description="暂无视频流，点击右上角发起实时视频" />
    </div>

    <el-dialog v-model="showStartDialog" title="发起实时视频" width="440px">
      <el-form label-width="80px">
        <el-form-item label="终端号">
          <el-input v-model="startForm.vehicle_id" placeholder="输入终端手机号" />
        </el-form-item>
        <el-form-item label="逻辑通道">
          <el-input-number v-model="startForm.channel" :min="1" :max="36" />
        </el-form-item>
        <el-form-item label="媒体类型">
          <el-select v-model="startForm.media_type">
            <el-option label="音视频" :value="0" />
            <el-option label="视频" :value="1" />
            <el-option label="音频" :value="2" />
          </el-select>
        </el-form-item>
        <el-form-item label="码流类型">
          <el-select v-model="startForm.stream_type">
            <el-option label="主码流" :value="0" />
            <el-option label="子码流" :value="1" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showStartDialog = false">取消</el-button>
        <el-button type="primary" @click="startStream">确定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showPlaybackDialog" title="历史录像回放" width="440px">
      <el-form label-width="80px">
        <el-form-item label="终端号">
          <el-input v-model="playbackForm.vehicle_id" placeholder="输入终端手机号" />
        </el-form-item>
        <el-form-item label="逻辑通道">
          <el-input-number v-model="playbackForm.channel" :min="1" :max="36" />
        </el-form-item>
        <el-form-item label="开始时间">
          <el-date-picker v-model="playbackForm.start_time" type="datetime" placeholder="选择开始时间" value-format="YYYYMMDDHHmmss" style="width: 100%;" />
        </el-form-item>
        <el-form-item label="结束时间">
          <el-date-picker v-model="playbackForm.end_time" type="datetime" placeholder="选择结束时间" value-format="YYYYMMDDHHmmss" style="width: 100%;" />
        </el-form-item>
        <el-form-item label="回放方式">
          <el-select v-model="playbackForm.playback_mode">
            <el-option label="正常回放" :value="0" />
            <el-option label="快进回放" :value="1" />
            <el-option label="关键帧回放" :value="2" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showPlaybackDialog = false">取消</el-button>
        <el-button type="primary" @click="startPlayback">开始回放</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showDownloadDialog" title="录像下载" width="440px">
      <el-form label-width="80px">
        <el-form-item label="终端号">
          <el-input v-model="downloadForm.vehicle_id" placeholder="输入终端手机号" />
        </el-form-item>
        <el-form-item label="逻辑通道">
          <el-input-number v-model="downloadForm.channel" :min="1" :max="36" />
        </el-form-item>
        <el-form-item label="开始时间">
          <el-date-picker v-model="downloadForm.start_time" type="datetime" placeholder="选择开始时间" value-format="YYYYMMDDHHmmss" style="width: 100%;" />
        </el-form-item>
        <el-form-item label="结束时间">
          <el-date-picker v-model="downloadForm.end_time" type="datetime" placeholder="选择结束时间" value-format="YYYYMMDDHHmmss" style="width: 100%;" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showDownloadDialog = false">取消</el-button>
        <el-button type="primary" @click="startDownload">开始下载</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// AUTO-FIX-2026-06-29: Video.vue 重构——拆分为 VideoGrid + QualityPanel + VideoToolbar 子组件
// 保留播放器核心逻辑（FLV/HLS/WebRTC 初始化、统计、重连），移除内联模板重复代码
import { ref, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { VideoCamera, Loading } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { videoApi } from '../api'
import flvjs from 'flv.js'
import Hls from 'hls.js'
import VideoGrid from '../components/video/VideoGrid.vue'
import QualityPanel from '../components/video/QualityPanel.vue'
import VideoToolbar from '../components/video/VideoToolbar.vue'

const streams = ref([])
const showStartDialog = ref(false)
const showPlaybackDialog = ref(false)
const showDownloadDialog = ref(false)
const streamSchema = ref('flv')
const layoutMode = ref(4)
const ptzSpeed = ref(4)
const autoAdaptive = ref(true)
// AUTO-FIX-2026-06-29 [P1-5]: 16 画面激活管理——非激活画面暂停拉流
const activeStreamId = ref('')

// AUTO-FIX-2026-07-02 [P1-3.1]: WebRTC STUN/TURN 服务器配置
// 默认使用 Google 公开 STUN 服务器，可通过 localStorage 覆盖配置
// 在 NAT 环境下必须有 STUN/TURN 服务器才能建立 P2P 连接
const webrtcIceServers = (() => {
  try {
    const saved = localStorage.getItem('webrtc_ice_servers')
    if (saved) return JSON.parse(saved)
  } catch (e) { /* ignore parse error */ }
  // 默认配置：Google 公开 STUN 服务器
  return [
    { urls: 'stun:stun.l.google.com:19302' },
    { urls: 'stun:stun1.l.google.com:19302' },
  ]
})()

// getWebRTCConfig 返回 RTCPeerConnection 配置对象
function getWebRTCConfig() {
  return {
    iceServers: webrtcIceServers,
    // 优先使用 STUN，TURN 仅在 STUN 失败时使用
    iceTransportPolicy: 'all',
  }
}

const startForm = ref({
  vehicle_id: '',
  channel: 1,
  media_type: 0,
  stream_type: 0,
})

const playbackForm = ref({
  vehicle_id: '',
  channel: 1,
  media_type: 0,
  stream_type: 0,
  playback_mode: 0,
  start_time: '',
  end_time: '',
})

const downloadForm = ref({
  vehicle_id: '',
  channel: 1,
  media_type: 0,
  stream_type: 0,
  playback_mode: 0,
  start_time: '',
  end_time: '',
})

const flvPlayers = {}
const hlsPlayers = {}
const webrtcConnections = {}
const retryTimers = {}
const statsTimers = {}
const webrtcOfferData = {}
const attachedVideoEls = new WeakSet()

// AUTO-FIX-2026-07-02 [P3-3.3]: 16 画面激活切换延迟优化
// 播放器缓存：停用时暂停而非销毁，激活时恢复而非重建，避免 2-3 秒重新拉流延迟
const playerCache = {}
const PLAYER_CACHE_SIZE = 4
const webrtcRemoteStreams = {} // 存储 WebRTC 远端流，用于重新挂载

const bufferStates = ref({})
const qualityStats = ref({})
const retryCounts = ref({})

// FIXED: [视频性能] 最大并发流限制，防止浏览器崩溃 [2026-07-17]
// 1/4/9 画面模式下最多 9 路同时拉流，16 画面模式由激活机制控制
const MAX_CONCURRENT_STREAMS = 9

async function startStream() {
  // FIXED: [视频性能] 检查并发流限制，防止浏览器崩溃 [2026-07-17]
  if (streams.value.length >= MAX_CONCURRENT_STREAMS) {
    ElMessage.warning(`已达到最大并发流数限制（${MAX_CONCURRENT_STREAMS}路），请先停止部分视频`)
    return
  }
  try {
    const res = await videoApi.startStream({
      vehicle_id: startForm.value.vehicle_id,
      logic_channel: startForm.value.channel,
      media_type: startForm.value.media_type,
      stream_type: startForm.value.stream_type,
    })
    if (res.code === 0 && res.data) {
      const streamId = Date.now().toString()
      streams.value.push({
        id: streamId,
        stream_id: res.data.stream_id || '',
        vehicle_id: startForm.value.vehicle_id,
        channel: startForm.value.channel,
        stream_type: startForm.value.stream_type,
        rtp_mode: 'udp',
        url: res.data.flv_url || res.data.hls_url || res.data.rtsp_url || '',
        flv_url: res.data.flv_url || '',
        hls_url: res.data.hls_url || '',
        rtsp_url: res.data.rtsp_url || '',
        status: 'connecting',
        error: '',
      })

      // P1-5: 16 画面模式下新增流时，自动激活新流并暂停其他流
      if (layoutMode.value === 16 && activeStreamId.value) {
        destroyPlayer(activeStreamId.value)
        const oldStream = streams.value.find(s => s.id === activeStreamId.value)
        if (oldStream) {
          oldStream.status = 'connecting'
          oldStream.error = ''
        }
      }
      if (layoutMode.value === 16) {
        activeStreamId.value = streamId
      }

      await nextTick()
      initPlayer(streamId, res.data)
    } else {
      ElMessage.error(res.message || '无法获取视频流地址')
    }
    showStartDialog.value = false
  } catch (e) {
    console.error('Start stream failed:', e)
    ElMessage.error('发起视频失败')
  }
}

function initPlayer(streamId, data) {
  if (streamSchema.value === 'flv' && data.flv_url) {
    initFlvPlayer(streamId, data.flv_url)
  } else if (streamSchema.value === 'hls' && data.hls_url) {
    initHlsPlayer(streamId, data.hls_url)
  } else if (streamSchema.value === 'webrtc') {
    initWebRTCPlayer(streamId, data)
  }
}

function initFlvPlayer(streamId, url) {
  if (!flvjs.isSupported()) {
    updateStreamStatus(streamId, 'error', '浏览器不支持FLV播放')
    return
  }
  const videoEl = document.getElementById(`video-${streamId}`)
  if (!videoEl) return

  const player = flvjs.createPlayer({
    type: 'flv',
    url: url,
    isLive: true,
    hasAudio: true,
    hasVideo: true,
  }, {
    enableStashBuffer: false,
    stashInitialSize: 128,
    autoCleanupSourceBuffer: true,
    autoCleanupMaxBackwardDuration: 3,
    autoCleanupMinBackwardDuration: 2,
    lazyLoadMaxDuration: 3 * 60,
    seekType: 'range',
  })

  player.attachMediaElement(videoEl)
  player.load()
  attachVideoEvents(streamId, videoEl)

  player.on(flvjs.Events.ERROR, (errorType, errorDetail, errorInfo) => {
    console.error('FLV player error:', errorType, errorDetail)
    updateStreamStatus(streamId, 'error', `播放错误: ${errorDetail}`)
    scheduleRetry(streamId, url, 'flv')
  })

  player.on(flvjs.Events.LOADING_COMPLETE, () => {
    updateStreamStatus(streamId, 'ended', '直播流结束')
  })

  player.play().then(() => {
    updateStreamStatus(streamId, 'playing', '')
  }).catch(() => {
    updateStreamStatus(streamId, 'error', '自动播放被阻止，请手动点击播放')
  })

  flvPlayers[streamId] = player
  startFlvStats(streamId, player)
}

function initHlsPlayer(streamId, url) {
  const videoEl = document.getElementById(`video-${streamId}`)
  if (!videoEl) return
  attachVideoEvents(streamId, videoEl)

  if (Hls.isSupported()) {
    const hls = new Hls({
      enableWorker: true,
      lowLatencyMode: true,
      maxBufferLength: 10,
      maxMaxBufferLength: 30,
      liveSyncDurationCount: 3,
      liveMaxLatencyDurationCount: 6,
    })

    hls.loadSource(url)
    hls.attachMedia(videoEl)

    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      videoEl.play().then(() => {
        updateStreamStatus(streamId, 'playing', '')
      }).catch(() => {
        updateStreamStatus(streamId, 'error', '自动播放被阻止，请手动点击播放')
      })
    })

    hls.on(Hls.Events.ERROR, (event, data) => {
      console.error('HLS player error:', data.type, data.details)
      if (data.fatal) {
        switch (data.type) {
          case Hls.ErrorTypes.NETWORK_ERROR:
            updateStreamStatus(streamId, 'error', '网络错误，尝试重连...')
            hls.startLoad()
            break
          case Hls.ErrorTypes.MEDIA_ERROR:
            updateStreamStatus(streamId, 'error', '媒体错误，尝试恢复...')
            hls.recoverMediaError()
            break
          default:
            updateStreamStatus(streamId, 'error', 'HLS播放失败')
            destroyHlsPlayer(streamId)
            break
        }
      }
    })

hlsPlayers[streamId] = hls
// AUTO-FIX-2026-07-02 [P3-3.2]: 传入 hls player 以获取 bitrate 统计
startHlsStats(streamId, hls)
  } else if (videoEl.canPlayType('application/vnd.apple.mpegurl')) {
videoEl.src = url
// AUTO-FIX-2026-07-02 [P3-3.2]: 原生 HLS 无 hls.js 实例，传 null
startHlsStats(streamId, null)
    videoEl.addEventListener('loadedmetadata', () => {
      videoEl.play().then(() => {
        updateStreamStatus(streamId, 'playing', '')
      }).catch(() => {
        updateStreamStatus(streamId, 'error', '自动播放被阻止')
      })
    })
  } else {
    updateStreamStatus(streamId, 'error', '浏览器不支持HLS播放')
  }
}

async function initWebRTCPlayer(streamId, data) {
  const videoEl = document.getElementById(`video-${streamId}`)
  if (!videoEl) return

  const streamIdFromServer = data.stream_id || ''
  if (!streamIdFromServer) {
    updateStreamStatus(streamId, 'error', '缺少stream_id')
    return
  }

  webrtcOfferData[streamId] = data
  retryCounts.value[streamId] = 0
  attachVideoEvents(streamId, videoEl)
  await createWebRTCPeerConnection(streamId, streamIdFromServer)
}

// AUTO-FIX-2026-07-01 [P0-1]: 等待 ICE candidate 收集完成。
// setLocalDescription 后 ICE agent 异步收集 candidate，立即读取的 offer.sdp 不含任何
// candidate，server 收到的 SDP 无法建立连通性 → WebRTC 必失败。
// 等待 iceGatheringState === 'complete' 后，pc.localDescription.sdp 包含全部本地 candidate。
// 设超时保护：STUN 不可达或仅 host candidate 时 3s 后强制发送已收集的 candidate（优于无）。
function waitForIceGatheringComplete(pc, timeoutMs) {
  return new Promise((resolve) => {
    if (pc.iceGatheringState === 'complete') {
      resolve()
      return
    }
    let done = false
    const finish = () => {
      if (done) return
      done = true
      pc.removeEventListener('icegatheringstatechange', check)
      resolve()
    }
    const check = () => {
      if (pc.iceGatheringState === 'complete') finish()
    }
    pc.addEventListener('icegatheringstatechange', check)
    setTimeout(finish, timeoutMs)
  })
}

async function createWebRTCPeerConnection(streamId, streamIdFromServer) {
  const videoEl = document.getElementById(`video-${streamId}`)
  if (!videoEl) return

  try {
    // AUTO-FIX-2026-07-02 [P1-3.1]: 传入 ICE 服务器配置，确保 NAT 环境下可建立连接
    const pc = new RTCPeerConnection(getWebRTCConfig())
pc.ontrack = (event) => {
webrtcRemoteStreams[streamId] = event.streams[0] // AUTO-FIX-2026-07-02 [P3-3.3]: 缓存远端流用于重新挂载
if (videoEl) videoEl.srcObject = event.streams[0]
updateStreamStatus(streamId, 'playing', '')
retryCounts.value[streamId] = 0
}
    pc.oniceconnectionstatechange = () => {
      if (pc.iceConnectionState === 'failed') {
        handleWebRTCFailure(streamId, streamIdFromServer, 'ICE连接失败')
      }
    }

    pc.addTransceiver('video', { direction: 'recvonly' })
    pc.addTransceiver('audio', { direction: 'recvonly' })

    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)

    // AUTO-FIX-2026-07-01 [P0-1]: 等待 ICE 收集完成，确保发送给 server 的 SDP 含 candidate。
    // 不等待时 offer.sdp 无 candidate，WebRTC 协商成功但媒体无法连通（黑屏）。
    await waitForIceGatheringComplete(pc, 3000)

    const res = await videoApi.webrtc({
      app: 'rtp',
      stream: streamIdFromServer,
      sdp_offer: pc.localDescription.sdp,
    })

    if (res.code === 0 && res.data && res.data.sdp_answer) {
      await pc.setRemoteDescription({ type: 'answer', sdp: res.data.sdp_answer })
      webrtcConnections[streamId] = pc
      startQualityStats(streamId, pc)
    } else if (res.data && res.data.fallback) {
      pc.close()
      retryCounts.value[streamId] = 3
      updateStreamStatus(streamId, 'connecting', 'WebRTC不可用，切换到FLV')
      fallbackFromWebRTC(streamId, res.data.fallback)
    } else {
      pc.close()
      handleWebRTCFailure(streamId, streamIdFromServer, 'WebRTC协商失败')
    }
  } catch (e) {
    console.error('WebRTC init failed:', e)
    handleWebRTCFailure(streamId, streamIdFromServer, 'WebRTC连接失败')
  }
}

function handleWebRTCFailure(streamId, streamIdFromServer, reason) {
  const count = (retryCounts.value[streamId] || 0) + 1
  retryCounts.value[streamId] = count
  destroyWebRTCPlayer(streamId)

  if (count <= 3) {
    updateStreamStatus(streamId, 'connecting', `${reason}，第${count}次重连...`)
    retryTimers[streamId] = setTimeout(() => {
      delete retryTimers[streamId]
      createWebRTCPeerConnection(streamId, streamIdFromServer)
    }, 3000)
  } else {
    updateStreamStatus(streamId, 'connecting', 'WebRTC多次失败，切换到FLV')
    fallbackFromWebRTC(streamId, null)
  }
}

function fallbackFromWebRTC(streamId, fallback) {
  try {
    const data = webrtcOfferData[streamId] || {}
    const flvUrl = (fallback && fallback.flv_url) || data.flv_url
    const hlsUrl = (fallback && fallback.hls_url) || data.hls_url
    if (flvUrl) {
      initFlvPlayer(streamId, flvUrl)
    } else if (hlsUrl) {
      initHlsPlayer(streamId, hlsUrl)
    } else {
      updateStreamStatus(streamId, 'error', 'WebRTC失败且无可用降级地址')
    }
  } catch (e) {
    console.error('WebRTC fallback failed:', e)
    updateStreamStatus(streamId, 'error', '降级失败')
  }
}

function scheduleRetry(streamId, url, type) {
  if (retryTimers[streamId]) return
  retryTimers[streamId] = setTimeout(() => {
    delete retryTimers[streamId]
    if (type === 'flv') {
      destroyFlvPlayer(streamId)
      initFlvPlayer(streamId, url)
    } else if (type === 'hls') {
      destroyHlsPlayer(streamId)
      initHlsPlayer(streamId, url)
    }
  }, 5000)
}

function updateStreamStatus(streamId, status, error) {
  const stream = streams.value.find(s => s.id === streamId)
  if (stream) {
    stream.status = status
    stream.error = error
  }
}

function destroyFlvPlayer(streamId) {
  const player = flvPlayers[streamId]
  if (player) {
    try {
      player.pause()
      player.unload()
      player.detachMediaElement()
      player.destroy()
    } catch {}
    delete flvPlayers[streamId]
  }
  if (retryTimers[streamId]) {
    clearTimeout(retryTimers[streamId])
    delete retryTimers[streamId]
  }
}

function destroyHlsPlayer(streamId) {
  const hls = hlsPlayers[streamId]
  if (hls) {
    try {
      hls.destroy()
    } catch {}
    delete hlsPlayers[streamId]
  }
  if (retryTimers[streamId]) {
    clearTimeout(retryTimers[streamId])
    delete retryTimers[streamId]
  }
}

function destroyWebRTCPlayer(streamId) {
  const pc = webrtcConnections[streamId]
  if (pc) {
    try {
      pc.close()
    } catch {}
    delete webrtcConnections[streamId]
  }
  if (statsTimers[streamId]) {
    clearInterval(statsTimers[streamId])
    delete statsTimers[streamId]
  }
  if (retryTimers[streamId]) {
    clearTimeout(retryTimers[streamId])
    delete retryTimers[streamId]
  }
}

function destroyPlayer(streamId) {
destroyFlvPlayer(streamId)
destroyHlsPlayer(streamId)
destroyWebRTCPlayer(streamId)
// AUTO-FIX-2026-07-02 [P3-3.3]: 同步清理播放器缓存
destroyCachedPlayer(streamId)
delete webrtcRemoteStreams[streamId]
if (statsTimers[streamId]) {
clearInterval(statsTimers[streamId])
delete statsTimers[streamId]
}
delete bufferStates.value[streamId]
delete qualityStats.value[streamId]
delete retryCounts.value[streamId]
delete webrtcOfferData[streamId]
}

// AUTO-FIX-2026-07-02 [P3-3.3]: 16 画面激活切换延迟优化——播放器缓存机制
// 暂停并缓存播放器（而非销毁），保留流连接以快速恢复
function pauseAndCachePlayer(streamId) {
// FLV
if (flvPlayers[streamId]) {
try {
flvPlayers[streamId].pause()
flvPlayers[streamId].detachMediaElement()
} catch {}
playerCache[streamId] = { type: 'flv', player: flvPlayers[streamId] }
delete flvPlayers[streamId]
}
// HLS
else if (hlsPlayers[streamId]) {
try {
hlsPlayers[streamId].stopLoad()
hlsPlayers[streamId].detachMedia()
} catch {}
playerCache[streamId] = { type: 'hls', player: hlsPlayers[streamId] }
delete hlsPlayers[streamId]
}
// WebRTC
else if (webrtcConnections[streamId]) {
// 保留 PC 连接，仅清除视频元素引用
playerCache[streamId] = { type: 'webrtc', player: webrtcConnections[streamId] }
delete webrtcConnections[streamId]
}

// 清除统计定时器（恢复时重建）
if (statsTimers[streamId]) {
clearInterval(statsTimers[streamId])
delete statsTimers[streamId]
}

// 缓存超出上限时销毁最旧条目
const cacheKeys = Object.keys(playerCache)
if (cacheKeys.length > PLAYER_CACHE_SIZE) {
destroyCachedPlayer(cacheKeys[0])
}
}

// tryResumePlayer 尝试从缓存恢复播放器，返回 true 表示成功恢复
function tryResumePlayer(streamId) {
const cached = playerCache[streamId]
if (!cached) return false

const videoEl = document.getElementById(`video-${streamId}`)
if (!videoEl) return false

delete playerCache[streamId]

if (cached.type === 'flv') {
try {
cached.player.attachMediaElement(videoEl)
cached.player.load()
cached.player.play()
flvPlayers[streamId] = cached.player
startFlvStats(streamId, cached.player)
attachVideoEvents(streamId, videoEl)
updateStreamStatus(streamId, 'playing', '')
return true
} catch {
destroyCachedPlayer(streamId)
return false
}
} else if (cached.type === 'hls') {
try {
cached.player.attachMedia(videoEl)
cached.player.startLoad()
hlsPlayers[streamId] = cached.player
startHlsStats(streamId, cached.player)
attachVideoEvents(streamId, videoEl)
updateStreamStatus(streamId, 'playing', '')
return true
} catch {
destroyCachedPlayer(streamId)
return false
}
} else if (cached.type === 'webrtc') {
try {
webrtcConnections[streamId] = cached.player
// 重新挂载远端流
if (webrtcRemoteStreams[streamId]) {
videoEl.srcObject = webrtcRemoteStreams[streamId]
}
startQualityStats(streamId, cached.player)
attachVideoEvents(streamId, videoEl)
updateStreamStatus(streamId, 'playing', '')
return true
} catch {
destroyCachedPlayer(streamId)
return false
}
}

return false
}

// destroyCachedPlayer 销毁缓存中的播放器
function destroyCachedPlayer(streamId) {
const cached = playerCache[streamId]
if (!cached) return
delete playerCache[streamId]

try {
if (cached.type === 'flv') {
cached.player.destroy()
} else if (cached.type === 'hls') {
cached.player.destroy()
} else if (cached.type === 'webrtc') {
cached.player.close()
delete webrtcRemoteStreams[streamId]
}
} catch {}
}

async function stopStream(stream) {
  try {
    await videoApi.stopStream({
      stream_id: stream.stream_id || '',
      vehicle_id: stream.vehicle_id,
      logic_channel: stream.channel,
    })
  } catch (e) {
    console.error('Stop stream failed:', e)
  }
  destroyPlayer(stream.id)
  streams.value = streams.value.filter(s => s.id !== stream.id)
  // P1-5: 停止的是激活流时，清空激活状态或切换到第一个流
  if (activeStreamId.value === stream.id) {
    activeStreamId.value = layoutMode.value === 16 && streams.value.length > 0 ? streams.value[0].id : ''
  }
}

function switchChannel(stream, channel) {
  destroyPlayer(stream.id)
  stream.channel = channel
  stream.status = 'connecting'
  stream.error = ''
  startStreamForExisting(stream)
}

// AUTO-FIX-2026-07-02: 双码流无缝切换 - 使用 /media/switch-stream API
// 保留 SSRC/时间戳/StartTime，播放状态天然保留（project_memory 双码流切换经验）
// 切换时显示 loading 状态，完成后更新 stream_type 并刷新播放
async function switchStreamType(stream, newStreamType) {
  if (stream.stream_type === newStreamType) return
  if (stream.switching) return // 防止重复点击
  stream.switching = true
  stream.status = 'connecting'
  stream.error = ''
  try {
    const res = await videoApi.switchStream({
      vehicle_id: stream.vehicle_id,
      logic_channel: stream.channel,
      stream_type: newStreamType,
    })
    if (res.code === 0) {
      // 后端复用 RTP 端口，仅更新 StreamType，前端无需重建播放器
      stream.stream_type = newStreamType
      stream.status = 'playing'
      ElMessage.success(`已切换到${newStreamType === 0 ? '主码流' : '子码流'}`)
    } else {
      // 后端切换失败，回退到旧方式（销毁重建）
      console.warn('Seamless switch failed, fallback to rebuild:', res.message)
      destroyPlayer(stream.id)
      stream.stream_type = newStreamType
      stream.status = 'connecting'
      bufferStates.value[stream.id] = false
      startStreamForExisting(stream)
    }
  } catch (e) {
    console.error('Switch stream type failed:', e)
    // 回退到旧方式
    destroyPlayer(stream.id)
    stream.stream_type = newStreamType
    stream.status = 'connecting'
    bufferStates.value[stream.id] = false
    startStreamForExisting(stream)
  } finally {
    stream.switching = false
  }
}

async function startStreamForExisting(stream) {
  try {
    const res = await videoApi.startStream({
      vehicle_id: stream.vehicle_id,
      logic_channel: stream.channel,
      media_type: 0,
      stream_type: stream.stream_type != null ? stream.stream_type : 0,
    })
    if (res.code === 0 && res.data) {
      stream.stream_id = res.data.stream_id || ''
      stream.url = res.data.flv_url || res.data.hls_url || ''
      stream.flv_url = res.data.flv_url || ''
      stream.hls_url = res.data.hls_url || ''
      await nextTick()
      initPlayer(stream.id, res.data)
    } else {
      stream.status = 'error'
      stream.error = '无法获取视频流'
    }
  } catch (e) {
    stream.status = 'error'
    stream.error = e.message || '请求失败'
  }
}

function attachVideoEvents(streamId, videoEl) {
  if (!videoEl || attachedVideoEls.has(videoEl)) return
  attachedVideoEls.add(videoEl)

  videoEl.addEventListener('waiting', () => {
    bufferStates.value[streamId] = true
  })
  videoEl.addEventListener('playing', () => {
    bufferStates.value[streamId] = false
  })
  videoEl.addEventListener('canplay', () => {
    bufferStates.value[streamId] = false
  })
  videoEl.addEventListener('loadedmetadata', () => {
    const w = videoEl.videoWidth || 0
    const h = videoEl.videoHeight || 0
    if (w && h) {
      updateQualityStats(streamId, { resolution: `${w}x${h}` })
    }
  })
}

function updateQualityStats(streamId, patch) {
  const cur = qualityStats.value[streamId] || {}
  qualityStats.value[streamId] = { ...cur, ...patch }
}

function startQualityStats(streamId, pc) {
  if (statsTimers[streamId]) clearInterval(statsTimers[streamId])
  let lastBytes = 0
  let lastTs = 0
  let lastPacketsLost = 0
  let lastPacketsReceived = 0
  let lowQualityCount = 0
  statsTimers[streamId] = setInterval(async () => {
    try {
      if (!pc || pc.connectionState === 'closed') {
        clearInterval(statsTimers[streamId])
        delete statsTimers[streamId]
        return
      }
      const stats = await pc.getStats()
      let bitrate = null
      let fps = null
      let packetsLost = null
      let lossRate = null
      stats.forEach((report) => {
        if (report.type === 'inbound-rtp' && report.kind === 'video') {
          if (lastTs && report.timestamp > lastTs && report.bytesReceived != null) {
            const deltaBytes = report.bytesReceived - lastBytes
            const deltaSec = (report.timestamp - lastTs) / 1000
            if (deltaSec > 0) {
              bitrate = Math.round((deltaBytes * 8) / 1000 / deltaSec)
            }
          }
          lastBytes = report.bytesReceived || 0
          lastTs = report.timestamp
          if (report.framesPerSecond != null) fps = Math.round(report.framesPerSecond)
          if (report.packetsLost != null) packetsLost = report.packetsLost
          const curLost = report.packetsLost || 0
          const curRecv = report.packetsReceived || 0
          const deltaLost = curLost - lastPacketsLost
          const deltaRecv = curRecv - lastPacketsReceived
          lastPacketsLost = curLost
          lastPacketsReceived = curRecv
          if (deltaLost >= 0 && deltaRecv >= 0 && (deltaLost + deltaRecv) > 0) {
            lossRate = Math.round((deltaLost / (deltaLost + deltaRecv)) * 1000) / 10
          }
        }
      })
      updateQualityStats(streamId, { bitrate, fps, packetsLost, lossRate })

      if (autoAdaptive.value) {
        const poor = (lossRate != null && lossRate > 5) || (bitrate != null && bitrate < 100)
        if (poor) {
          lowQualityCount++
          if (lowQualityCount >= 3) {
            const stream = streams.value.find(s => s.id === streamId)
            if (stream && stream.stream_type === 0) {
              ElMessage.warning('网络质量下降，自动切换到子码流')
              switchStreamType(stream, 1)
              lowQualityCount = 0
            }
          }
        } else {
          lowQualityCount = 0
        }
      }
    } catch (e) {
      console.error('getStats failed:', e)
    }
  }, 1000)
}

function toggleFullscreen(streamId) {
  const videoEl = document.getElementById(`video-${streamId}`)
  if (!videoEl) return
  if (document.fullscreenElement) {
    document.exitFullscreen()
  } else {
    videoEl.requestFullscreen()
  }
}

function screenshot(stream) {
  const videoEl = document.getElementById(`video-${stream.id}`)
  if (!videoEl || !videoEl.videoWidth) {
    ElMessage.warning('视频未就绪，无法截图')
    return
  }
  const canvas = document.createElement('canvas')
  canvas.width = videoEl.videoWidth
  canvas.height = videoEl.videoHeight
  canvas.getContext('2d').drawImage(videoEl, 0, 0)
  const link = document.createElement('a')
  link.download = `${stream.vehicle_id}_ch${stream.channel}_${Date.now()}.png`
  link.href = canvas.toDataURL('image/png')
  link.click()
  ElMessage.success('截图已保存')
}

// RTP 传输模式切换（UDP/TCP）— 公网/NAT 环境下 UDP 不通时可切 TCP（1078-2022）
async function toggleStreamMode(stream) {
  const next = stream.rtp_mode === 'tcp' ? 'udp' : 'tcp'
  try {
    const res = await videoApi.setStreamMode({ stream_id: stream.id, mode: next })
    if (res.code === 0) {
      stream.rtp_mode = next
      ElMessage.success(`已切换为 ${next.toUpperCase()} 模式`)
    } else {
      ElMessage.error(res.message || '模式切换失败')
    }
  } catch (e) {
    console.error('Toggle stream mode failed:', e)
    ElMessage.error('模式切换失败')
  }
}

// FLV 播放器质量统计（flv.js statisticsInfo + VideoPlaybackQuality API）
// AUTO-FIX-2026-07-02 [P3-3.2]: 修复码率计算公式 + 补全 lossRate 统计
//   - 码率：speed(KB/s) × 8 = kbps（原公式 totalKbytes*8/speed 计算结果为秒数而非码率）
//   - 丢帧率：使用 getVideoPlaybackQuality() API（与 HLS 统计一致），
//     计算 droppedVideoFrames / (totalVideoFrames + droppedVideoFrames) × 100%
function startFlvStats(streamId, player) {
  if (statsTimers[streamId]) clearInterval(statsTimers[streamId])
  let lastFrames = 0
  let lastDropped = 0
  let lastTs = 0
  statsTimers[streamId] = setInterval(() => {
    try {
      if (!player || player.isPaused?.()) return
      const s = player.statisticsInfo
      const videoEl = document.getElementById(`video-${streamId}`)
      // 码率：flv.js speed 为当前下载速度 KB/s，×8 得 kbps
      const bitrate = s?.speed ? Math.round(s.speed * 8) : null
      // fps：优先用 flv.js 解码帧率，回退到 VideoPlaybackQuality API
      let fps = s?.decodedFPS != null ? Math.round(s.decodedFPS) : (s?.presentedFPS != null ? Math.round(s.presentedFPS) : null)
      // 丢帧率：使用 getVideoPlaybackQuality() API（与 HLS 一致）
      let lossRate = null
      let packetsLost = s?.dropped != null ? s.dropped : null
      const q = videoEl?.getVideoPlaybackQuality?.()
      if (q) {
        const now = Date.now()
        const curFrames = q.totalVideoFrames || 0
        const curDropped = q.droppedVideoFrames || 0
        if (lastTs > 0 && now > lastTs) {
          const deltaSec = (now - lastTs) / 1000
          const deltaFrames = curFrames - lastFrames
          const deltaDropped = curDropped - lastDropped
          if (deltaFrames > 0) {
            fps = fps ?? Math.round(deltaFrames / deltaSec)
          }
          const totalDelta = deltaFrames + deltaDropped
          if (totalDelta > 0) {
            lossRate = Math.round((deltaDropped / totalDelta) * 1000) / 10
          }
        }
        lastFrames = curFrames
        lastDropped = curDropped
        lastTs = now
        packetsLost = curDropped
      }
      updateQualityStats(streamId, {
        bitrate: bitrate,
        fps: fps,
        lossRate: lossRate,
        packetsLost: packetsLost,
      })
      if (videoEl) {
        updateQualityStats(streamId, { resolution: `${videoEl.videoWidth}x${videoEl.videoHeight}` })
      }
    } catch (e) { /* ignore */ }
  }, 1000)
}

// HLS 播放器质量统计（hls.js stats + video element + VideoPlaybackQuality API）
// AUTO-FIX-2026-07-02 [P3-3.2]: 补全 bitrate 统计（通过 hls.js currentLevel 或视频元素估算）
function startHlsStats(streamId, hlsPlayer) {
  if (statsTimers[streamId]) clearInterval(statsTimers[streamId])
  let lastFrames = 0
  let lastDropped = 0
  let lastTs = 0
  statsTimers[streamId] = setInterval(() => {
    try {
      const videoEl = document.getElementById(`video-${streamId}`)
      if (!videoEl) return
      // 码率：优先用 hls.js currentLevel.bitrate（bps），转为 kbps
      let bitrate = null
      if (hlsPlayer?.currentLevel?.bitrate) {
        bitrate = Math.round(hlsPlayer.currentLevel.bitrate / 1000)
      }
      const q = videoEl.getVideoPlaybackQuality?.()
      if (q) {
        const now = Date.now()
        const curFrames = q.totalVideoFrames || 0
        const curDropped = q.droppedVideoFrames || 0
        let fps = null
        let lossRate = null
        if (lastTs > 0 && now > lastTs) {
          const deltaSec = (now - lastTs) / 1000
          const deltaFrames = curFrames - lastFrames
          const deltaDropped = curDropped - lastDropped
          if (deltaFrames > 0) {
            fps = Math.round(deltaFrames / deltaSec)
          }
          const totalDelta = deltaFrames + deltaDropped
          if (totalDelta > 0) {
            lossRate = Math.round((deltaDropped / totalDelta) * 1000) / 10
          }
        }
        lastFrames = curFrames
        lastDropped = curDropped
        lastTs = now
        updateQualityStats(streamId, {
          bitrate: bitrate,
          fps: fps,
          lossRate: lossRate,
          packetsLost: curDropped,
        })
      }
      updateQualityStats(streamId, { resolution: `${videoEl.videoWidth}x${videoEl.videoHeight}` })
    } catch (e) { /* ignore */ }
  }, 1000)
}

async function ptzControl(stream, direction) {
  try {
    await videoApi.ptz({
      vehicle_id: stream.vehicle_id,
      logic_channel: stream.channel,
      direction: direction,
      speed: ptzSpeed.value,
    })
  } catch (e) {
    console.error('PTZ control failed:', e)
  }
}

async function startPlayback() {
  try {
    const res = await videoApi.playback({
      vehicle_id: playbackForm.value.vehicle_id,
      logic_channel: playbackForm.value.channel,
      media_type: playbackForm.value.media_type,
      stream_type: playbackForm.value.stream_type,
      playback_mode: playbackForm.value.playback_mode,
      start_time: playbackForm.value.start_time,
      end_time: playbackForm.value.end_time,
    })
    if (res.code === 0 && res.data) {
      const streamId = Date.now().toString()
      streams.value.push({
        id: streamId,
        stream_id: res.data.stream_id || '',
        vehicle_id: playbackForm.value.vehicle_id,
        channel: playbackForm.value.channel,
        stream_type: playbackForm.value.stream_type,
        rtp_mode: 'udp',
        url: res.data.flv_url || res.data.hls_url || '',
        flv_url: res.data.flv_url || '',
        hls_url: res.data.hls_url || '',
        rtsp_url: res.data.rtsp_url || '',
        status: 'connecting',
        error: '',
      })
      // P1-5: 16 画面模式下回放也遵守激活策略
      if (layoutMode.value === 16) {
        if (activeStreamId.value) destroyPlayer(activeStreamId.value)
        activeStreamId.value = streamId
      }
      await nextTick()
      initPlayer(streamId, res.data)
      ElMessage.success('回放请求已发送')
    } else {
      ElMessage.error(res.message || '回放请求失败')
    }
    showPlaybackDialog.value = false
  } catch (e) {
    console.error('Playback failed:', e)
    ElMessage.error('回放请求失败')
  }
}

async function startDownload() {
  try {
    const res = await videoApi.download({
      vehicle_id: downloadForm.value.vehicle_id,
      logic_channel: downloadForm.value.channel,
      media_type: downloadForm.value.media_type,
      stream_type: downloadForm.value.stream_type,
      playback_mode: downloadForm.value.playback_mode,
      start_time: downloadForm.value.start_time,
      end_time: downloadForm.value.end_time,
    })
    if (res.code === 0) {
      ElMessage.success('下载请求已发送')
    } else {
      ElMessage.error(res.message || '下载请求失败')
    }
    showDownloadDialog.value = false
  } catch (e) {
    console.error('Download failed:', e)
    ElMessage.error('下载请求失败')
  }
}

// AUTO-FIX-2026-06-29 [P1-5]: 16 画面激活管理
// 切换到 16 画面时，默认激活第一个流，销毁其他流的播放器
// 切换回 1/4/9 画面时，重新初始化所有流的播放器
watch(layoutMode, (newMode, oldMode) => {
  if (newMode === 16 && streams.value.length > 0) {
    // 切换到 16 画面：只保留第一个流的播放器，销毁其他
    const firstId = streams.value[0].id
    streams.value.forEach(s => {
      if (s.id !== firstId) {
        destroyPlayer(s.id)
        s.status = 'connecting'
        s.error = ''
      }
    })
    activeStreamId.value = firstId
  } else if (oldMode === 16 && newMode !== 16) {
    // 从 16 画面切回：清空激活状态，重新初始化所有流
    activeStreamId.value = ''
    streams.value.forEach(stream => {
      if (stream.url) {
        stream.status = 'connecting'
        stream.error = ''
        const data = { flv_url: stream.flv_url, hls_url: stream.hls_url, stream_id: stream.stream_id }
        nextTick(() => initPlayer(stream.id, data))
      }
    })
  }
})

// AUTO-FIX-2026-06-29 [P1-5]: 激活切换处理
// 点击非激活格子时，暂停旧激活流播放器（缓存以快速恢复），初始化/恢复新激活流播放器
// AUTO-FIX-2026-07-02 [P3-3.3]: 优化为播放器缓存机制，避免每次切换都销毁/重建
function handleActivate(streamId) {
if (streamId === activeStreamId.value) return

// 暂停并缓存旧激活流的播放器（而非销毁）
if (activeStreamId.value) {
pauseAndCachePlayer(activeStreamId.value)
const oldStream = streams.value.find(s => s.id === activeStreamId.value)
if (oldStream) {
oldStream.status = 'connecting'
oldStream.error = ''
}
}

activeStreamId.value = streamId

// 优先尝试从缓存恢复，失败则初始化新播放器
const stream = streams.value.find(s => s.id === streamId)
if (stream && stream.url) {
nextTick(() => {
if (!tryResumePlayer(stream.id)) {
const data = { flv_url: stream.flv_url, hls_url: stream.hls_url, stream_id: stream.stream_id }
initPlayer(stream.id, data)
}
})
}
}

watch(streamSchema, () => {
  streams.value.forEach(stream => {
    // P1-5: 16 画面模式下只重初始化激活流
    if (layoutMode.value === 16 && stream.id !== activeStreamId.value) return
    if (stream.url) {
      destroyPlayer(stream.id)
      stream.status = 'connecting'
      stream.error = ''
      const data = { flv_url: stream.flv_url, hls_url: stream.hls_url, stream_id: stream.stream_id }
      nextTick(() => initPlayer(stream.id, data))
    }
  })
})

// 网络中断自动重连：监听 online/offline 事件，恢复后自动重连所有活跃流（保留播放状态）
function handleOnline() {
  ElMessage.success('网络已恢复，正在重连视频流...')
  streams.value.forEach(stream => {
    // P1-5: 16 画面模式下只重连激活流
    if (layoutMode.value === 16 && stream.id !== activeStreamId.value) return
    if (stream.status === 'error' || stream.status === 'reconnecting') {
      startStreamForExisting(stream)
    }
  })
}
function handleOffline() {
  ElMessage.warning('网络已断开，视频将自动重连')
  streams.value.forEach(stream => {
    if (stream.status === 'playing') {
      updateStreamStatus(stream.id, 'reconnecting', '网络中断，等待重连')
    }
  })
}

onMounted(() => {
  window.addEventListener('online', handleOnline)
  window.addEventListener('offline', handleOffline)
})

onUnmounted(() => {
  window.removeEventListener('online', handleOnline)
  window.removeEventListener('offline', handleOffline)
  Object.keys(flvPlayers).forEach(id => destroyFlvPlayer(id))
  Object.keys(hlsPlayers).forEach(id => destroyHlsPlayer(id))
  Object.keys(webrtcConnections).forEach(id => destroyWebRTCPlayer(id))
  // AUTO-FIX-2026-07-02 [P3-3.3]: 清理播放器缓存和 WebRTC 远端流
  Object.keys(playerCache).forEach(id => destroyCachedPlayer(id))
  Object.keys(webrtcRemoteStreams).forEach(id => delete webrtcRemoteStreams[id])
  Object.keys(retryTimers).forEach(id => {
    clearTimeout(retryTimers[id])
  })
  Object.keys(statsTimers).forEach(id => {
    clearInterval(statsTimers[id])
  })
})
</script>

<style scoped>
.video-container {
  width: 100%;
  height: 220px;
  background: #0a0e17;
  border-radius: 8px;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
}
.video-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
}
.buffer-overlay {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  color: #fff;
  font-size: 12px;
  background: rgba(0, 0, 0, 0.5);
  padding: 8px 14px;
  border-radius: 6px;
  pointer-events: none;
}
.buffer-spinner {
  animation: buffer-spin 1s linear infinite;
}
@keyframes buffer-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
