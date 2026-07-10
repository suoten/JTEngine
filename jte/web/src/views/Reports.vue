<template>
  <div class="page-container">
    <div class="page-header">
      <h2>报表中心</h2>
      <el-button type="primary" @click="showGenerate = true">生成报表</el-button>
    </div>
    <div class="filter-bar">
      <el-select v-model="filterType" placeholder="报表类型" style="width: 140px" @change="fetchReports">
        <el-option label="全部" value="" />
        <el-option label="行驶报表" value="mileage" />
        <el-option label="报警统计" value="alarm" />
        <el-option label="在线率" value="online_rate" />
        <el-option label="超速统计" value="overspeed" />
      </el-select>
    </div>
    <el-table :data="filteredReports" stripe>
      <el-table-column prop="report_id" label="报表ID" min-width="180" />
      <el-table-column prop="type" label="类型" width="120">
        <template #default="{ row }">{{ typeLabels[row.type] || row.type }}</template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="80">
        <template #default="{ row }"><el-tag :type="row.status === 'completed' ? 'success' : 'warning'" size="small">{{ row.status === 'completed' ? '完成' : '生成中' }}</el-tag></template>
      </el-table-column>
      <el-table-column label="操作" width="180" fixed="right">
        <template #default="{ row }">
          <el-button type="primary" link size="small" @click="viewReport(row)">查看</el-button>
          <el-button type="success" link size="small" @click="exportCSV(row)">CSV</el-button>
          <el-button type="warning" link size="small" @click="exportPDF(row)">PDF</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="showGenerate" title="生成报表" width="480px">
      <el-form label-width="80px">
        <el-form-item label="报表类型">
          <el-select v-model="genForm.type">
            <el-option label="行驶报表" value="mileage" />
            <el-option label="报警统计" value="alarm" />
            <el-option label="在线率" value="online_rate" />
            <el-option label="超速统计" value="overspeed" />
          </el-select>
        </el-form-item>
        <el-form-item label="统计周期">
          <el-select v-model="genForm.period">
            <el-option label="日报" value="daily" />
            <el-option label="周报" value="weekly" />
            <el-option label="月报" value="monthly" />
          </el-select>
        </el-form-item>
        <el-form-item label="设备号码">
          <el-input v-model="genForm.phone" placeholder="选填，部分报表需要" />
        </el-form-item>
        <el-form-item label="日期范围">
          <el-date-picker v-model="genForm.range" type="daterange" range-separator="至" start-placeholder="开始" end-placeholder="结束" value-format="YYYY-MM-DDTHH:mm:ssZ" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showGenerate = false">取消</el-button>
        <el-button type="primary" @click="generate" :loading="generating">生成</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showDetail" title="报表详情" width="780px" @open="onDetailOpen">
      <div v-if="currentReport">
        <!-- AUTO-FIX-2026-06-30 [P1-8]: 报表统计卡片 -->
        <el-row :gutter="12" style="margin-bottom: 16px;">
          <el-col :span="6" v-for="stat in reportStats" :key="stat.label">
            <el-card shadow="never" class="stat-card">
              <div class="stat-value">{{ stat.value }}</div>
              <div class="stat-label">{{ stat.label }}</div>
            </el-card>
          </el-col>
        </el-row>

        <!-- AUTO-FIX-2026-06-30 [P1-8]: ECharts 可视化图表 -->
        <el-card v-if="chartReady" shadow="never" style="margin-bottom: 12px;">
          <template #header><span class="chart-title">{{ chartTitle }}</span></template>
          <div ref="chartRef" style="width: 100%; height: 280px;"></div>
        </el-card>

        <!-- 原始数据兜底（无图表时展示） -->
        <el-card v-if="!chartReady" shadow="never">
          <template #header><span class="chart-title">原始数据</span></template>
          <pre style="margin:0; font-size:12px; max-height:240px; overflow:auto;">{{ JSON.stringify(currentReport.data, null, 2) }}</pre>
        </el-card>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
// AUTO-FIX-2026-06-30 [P1-8]: 报表中心增加 ECharts 可视化（行驶/报警/在线率/超速）
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import * as echarts from 'echarts'
import { reportApi } from '../api'
import { ElMessage } from 'element-plus'

const reports = ref([])
const filterType = ref('')
const showGenerate = ref(false)
const showDetail = ref(false)
const generating = ref(false)
const currentReport = ref(null)
const genForm = ref({ type: 'mileage', period: 'daily', phone: '', range: [] })

const typeLabels = { mileage: '行驶报表', alarm: '报警统计', online_rate: '在线率', overspeed: '超速统计' }

// 图表状态
const chartRef = ref(null)
const chartReady = ref(false)
const chartTitle = ref('')
let chartInstance = null
let resizeHandler = null

const filteredReports = computed(() => {
  if (!filterType.value) return reports.value
  return reports.value.filter(r => r.type === filterType.value)
})

// AUTO-FIX-2026-06-30 [P1-8]: 按报表类型提取统计卡片
const reportStats = computed(() => {
  const d = currentReport.value?.data || {}
  const type = currentReport.value?.type
  if (type === 'mileage') {
    return [
      { label: '总里程', value: fmtNum(d.mileage) + (d.unit ? ' ' + d.unit : '') },
      { label: '轨迹点数', value: fmtNum(d.points) },
      { label: '设备号码', value: d.phone || '-' },
      { label: '行驶时长', value: d.duration ? fmtDuration(d.duration) : '-' },
    ]
  }
  if (type === 'alarm') {
    return [
      { label: '报警总数', value: fmtNum(d.total) },
      { label: '报警类型数', value: d.by_type ? Object.keys(d.by_type).length : 0 },
      { label: '严重报警', value: fmtNum(d.by_level?.critical ?? d.by_level?.serious) },
      { label: '已处理', value: fmtNum(d.handled ?? d.resolved) },
    ]
  }
  if (type === 'online_rate') {
    const rate = d.online_rate != null ? (d.online_rate * 100).toFixed(1) + '%' : '-'
    return [
      { label: '在线设备', value: fmtNum(d.online) },
      { label: '离线设备', value: fmtNum(d.offline) },
      { label: '在线率', value: rate },
      { label: '统计时长', value: d.hours ? d.hours + 'h' : '-' },
    ]
  }
  if (type === 'overspeed') {
    return [
      { label: '超速次数', value: fmtNum(d.overspeed_count ?? d.count) },
      { label: '设备号码', value: d.phone || '-' },
      { label: '最高速度', value: d.max_speed != null ? d.max_speed + ' km/h' : '-' },
      { label: '平均速度', value: d.avg_speed != null ? d.avg_speed + ' km/h' : '-' },
    ]
  }
  return []
})

function fmtNum(v) {
  if (v == null) return '-'
  const n = Number(v)
  if (isNaN(n)) return String(v)
  return n.toLocaleString('zh-CN')
}

function fmtDuration(sec) {
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return h > 0 ? `${h}h${m}m` : `${m}m`
}

async function fetchReports() {
  try {
    const res = await reportApi.getList()
    if (res.code === 0 && res.data) {
      reports.value = (res.data.items || []).map(r => ({
        report_id: r.report_id || r.id,
        type: r.type || 'unknown',
        status: r.status || 'completed',
        data: r.data || r,
      }))
    }
  } catch (e) { console.error(e) }
}

async function generate() {
  generating.value = true
  try {
    const params = {
      type: genForm.value.type,
      period: genForm.value.period,
      phone: genForm.value.phone || undefined,
    }
    if (genForm.value.range && genForm.value.range.length === 2) {
      params.start_time = genForm.value.range[0]
      params.end_time = genForm.value.range[1]
    }
    const res = await reportApi.generate(params)
    if (res.code === 0) {
      ElMessage.success('报表生成成功')
      showGenerate.value = false
      await fetchReports()
    }
  } catch (e) {
    console.error(e)
    ElMessage.error('报表生成失败')
  } finally {
    generating.value = false
  }
}

function viewReport(row) {
  currentReport.value = row
  showDetail.value = true
}

// AUTO-FIX-2026-06-30 [P1-8]: 弹窗打开后渲染图表（等待 DOM 就绪）
async function onDetailOpen() {
  chartReady.value = false
  await nextTick()
  renderReportChart()
}

// 按报表类型选择图表配置；数据不支持可视化时 chartReady 保持 false，回退原始数据
function renderReportChart() {
  if (!chartRef.value || !currentReport.value) return
  const type = currentReport.value.type
  const d = currentReport.value.data || {}
  let option = null

  if (type === 'alarm') {
    option = buildAlarmChart(d)
  } else if (type === 'mileage' || type === 'overspeed') {
    option = buildTrendBarChart(d, type)
  } else if (type === 'online_rate') {
    option = buildOnlineRateChart(d)
  }

  if (!option) {
    chartReady.value = false
    return
  }
  chartReady.value = true
  chartTitle.value = typeLabels[type] + ' · ' + (genForm.value.period === 'weekly' ? '周报' : genForm.value.period === 'monthly' ? '月报' : '日报')
  if (!chartInstance) {
    chartInstance = echarts.init(chartRef.value)
  }
  chartInstance.setOption(option, true)
}

// 报警统计：按类型饼图
function buildAlarmChart(d) {
  const byType = d.by_type || d.by_alarm_type
  if (!byType || typeof byType !== 'object') return null
  const data = Object.entries(byType).map(([name, value]) => ({ name: name || '未知', value: Number(value) || 0 }))
  if (!data.length) return null
  return {
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { bottom: 0, type: 'scroll' },
    series: [{
      type: 'pie',
      radius: ['40%', '70%'],
      center: ['50%', '45%'],
      avoidLabelOverlap: true,
      itemStyle: { borderRadius: 6, borderColor: '#fff', borderWidth: 2 },
      label: { show: true, formatter: '{b}\n{d}%' },
      data,
    }],
  }
}

// 行驶/超速：按日趋势柱状图
function buildTrendBarChart(d, type) {
  const daily = d.daily || d.trend || d.timeline || d.by_day
  if (!Array.isArray(daily) || !daily.length) return null
  const labels = daily.map(it => it.date || it.day || it.time || '')
  const values = daily.map(it => Number(type === 'overspeed' ? (it.count ?? it.overspeed_count) : (it.mileage ?? it.distance)) || 0)
  if (!labels.length) return null
  const name = type === 'overspeed' ? '超速次数' : '里程'
  return {
    tooltip: { trigger: 'axis' },
    grid: { left: 50, right: 20, top: 20, bottom: 40 },
    xAxis: { type: 'category', data: labels, axisLabel: { fontSize: 10, rotate: labels.length > 10 ? 30 : 0 } },
    yAxis: { type: 'value', name: type === 'overspeed' ? '次' : 'km' },
    series: [{
      name, type: 'bar', data: values,
      itemStyle: { color: type === 'overspeed' ? '#ef4444' : '#6366f1', borderRadius: [4, 4, 0, 0] },
      barMaxWidth: 32,
    }],
  }
}

// 在线率：折线图
function buildOnlineRateChart(d) {
  const timeline = d.timeline || d.timeseries || d.by_hour || d.series
  if (!Array.isArray(timeline) || !timeline.length) return null
  const labels = timeline.map(it => it.time || it.hour || it.date || '')
  const rates = timeline.map(it => {
    const r = it.online_rate != null ? it.online_rate : (it.rate != null ? it.rate : null)
    return r != null ? Number((r * 100).toFixed(2)) : null
  })
  if (!labels.length) return null
  return {
    tooltip: { trigger: 'axis', formatter: '{b}<br/>在线率: {c}%' },
    grid: { left: 50, right: 20, top: 20, bottom: 40 },
    xAxis: { type: 'category', data: labels, axisLabel: { fontSize: 10, rotate: labels.length > 10 ? 30 : 0 } },
    yAxis: { type: 'value', max: 100, axisLabel: { formatter: '{value}%' } },
    series: [{
      name: '在线率', type: 'line', smooth: true, data: rates,
      showSymbol: false,
      areaStyle: { color: 'rgba(34,197,94,0.18)' },
      lineStyle: { color: '#22c55e', width: 2 },
      markLine: { data: [{ yAxis: 95, lineStyle: { color: '#f59e0b', type: 'dashed' } }] },
    }],
  }
}

function exportCSV(row) {
  if (!row.data) return
  const data = row.data
  let csv = ''
  if (Array.isArray(data)) {
    if (data.length === 0) return
    const headers = Object.keys(data[0])
    csv = headers.join(',') + '\n'
    data.forEach(item => {
      csv += headers.map(h => JSON.stringify(item[h] ?? '')).join(',') + '\n'
    })
  } else {
    Object.entries(data).forEach(([k, v]) => {
      csv += `${k},${JSON.stringify(v)}\n`
    })
  }
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `report_${row.type}_${row.report_id}.csv`
  a.click()
  URL.revokeObjectURL(url)
}

// AUTO-FIX-2026-06-26: 第六轮遗留修复 - PDF 导出（浏览器原生打印，零依赖）
// 通过打开新窗口渲染报表 HTML 并调用 window.print()，用户可在打印对话框选择"另存为 PDF"
function exportPDF(row) {
  if (!row.data) {
    ElMessage.warning('报表数据为空')
    return
  }
  const data = row.data
  const typeLabel = typeLabels[row.type] || row.type
  const title = `${typeLabel} - ${row.report_id || ''}`
  const now = new Date().toLocaleString('zh-CN')

  let tableHTML = ''
  if (Array.isArray(data)) {
    if (data.length === 0) {
      tableHTML = '<p style="text-align:center;color:#999">暂无数据</p>'
    } else {
      const headers = Object.keys(data[0])
      tableHTML = '<table><thead><tr>' +
        headers.map(h => `<th>${escapeHTML(h)}</th>`).join('') +
        '</tr></thead><tbody>'
      data.forEach(item => {
        tableHTML += '<tr>' +
          headers.map(h => `<td>${escapeHTML(item[h] ?? '')}</td>`).join('') +
          '</tr>'
      })
      tableHTML += '</tbody></table>'
    }
  } else {
    tableHTML = '<table><tbody>'
    Object.entries(data).forEach(([k, v]) => {
      tableHTML += `<tr><th style="text-align:right;width:200px">${escapeHTML(k)}</th><td>${escapeHTML(v ?? '')}</td></tr>`
    })
    tableHTML += '</tbody></table>'
  }

  const html = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>${escapeHTML(title)}</title>
<style>
  * { box-sizing: border-box; }
  body { font-family: "Microsoft YaHei", "PingFang SC", sans-serif; margin: 20px; color: #333; }
  .report-header { text-align: center; margin-bottom: 24px; padding-bottom: 16px; border-bottom: 2px solid #409eff; }
  .report-header h1 { margin: 0 0 8px 0; font-size: 20px; color: #303133; }
  .report-header .meta { font-size: 12px; color: #909399; }
  table { width: 100%; border-collapse: collapse; margin-top: 12px; font-size: 12px; }
  th, td { border: 1px solid #ebeef5; padding: 8px 12px; text-align: left; }
  thead th { background: #f5f7fa; color: #606266; font-weight: 600; }
  tbody tr:nth-child(even) { background: #fafafa; }
  tbody tr:hover { background: #f0f7ff; }
  .footer { margin-top: 32px; text-align: center; font-size: 10px; color: #c0c4cc; border-top: 1px solid #ebeef5; padding-top: 8px; }
  @page { margin: 1.5cm; }
  @media print {
    body { margin: 0; }
    .no-print { display: none; }
  }
</style>
</head>
<body>
  <div class="report-header">
    <h1>${escapeHTML(title)}</h1>
    <div class="meta">生成时间：${now} | 类型：${escapeHTML(typeLabel)}</div>
  </div>
  ${tableHTML}
  <div class="footer">JTE 智能交通引擎 - 报表导出 ${now}</div>
  <script>
    window.onload = function() {
      setTimeout(function() {
        window.print();
      }, 300);
    };
    window.onafterprint = function() {
      window.close();
    };
  <\/script>
</body>
</html>`

  const printWindow = window.open('', '_blank')
  if (!printWindow) {
    ElMessage.warning('弹窗被浏览器拦截，请允许弹窗后重试')
    return
  }
  printWindow.document.open()
  printWindow.document.write(html)
  printWindow.document.close()
}

function escapeHTML(str) {
  if (str === null || str === undefined) return ''
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

onMounted(() => {
  fetchReports()
  // AUTO-FIX-2026-06-30 [P1-8]: 图表自适应窗口尺寸
  resizeHandler = () => chartInstance && chartInstance.resize()
  window.addEventListener('resize', resizeHandler)
})

onUnmounted(() => {
  if (resizeHandler) window.removeEventListener('resize', resizeHandler)
  if (chartInstance) { chartInstance.dispose(); chartInstance = null }
})
</script>

<style scoped>
.page-container { padding: 24px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 18px; color: var(--jte-text); }
.filter-bar { margin-bottom: 16px; }
/* AUTO-FIX-2026-06-30 [P1-8]: 报表统计卡片与图表样式 */
.stat-card { text-align: center; }
.stat-card .stat-value { font-size: 20px; font-weight: 700; color: var(--jte-accent); }
.stat-card .stat-label { font-size: 12px; color: var(--jte-text-muted); margin-top: 4px; }
.chart-title { font-weight: 500; font-size: 14px; }
</style>
