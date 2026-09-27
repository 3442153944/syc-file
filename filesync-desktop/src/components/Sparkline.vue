<script setup lang="ts">
// 极简趋势缩略图：仪表盘卡片角落用，不带坐标轴/网格线/交互，只给一眼看出涨跌。
// 完整可交互图表点进对应卡片看（见 MonitorHistoryChart.vue）。
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    points: number[]
    color: string
    min?: number
    max?: number
    width?: number
    height?: number
  }>(),
  { min: 0, max: 100, width: 72, height: 28 },
)

function scaleY(v: number): number {
  const span = props.max - props.min || 1
  const clamped = Math.min(props.max, Math.max(props.min, v))
  return props.height - ((clamped - props.min) / span) * props.height
}

const path = computed(() => {
  const n = props.points.length
  if (n < 2) return ''
  const stepX = props.width / (n - 1)
  return props.points
    .map((v, i) => `${i === 0 ? 'M' : 'L'}${(i * stepX).toFixed(1)},${scaleY(v).toFixed(1)}`)
    .join(' ')
})

const lastPoint = computed(() => {
  const n = props.points.length
  if (n === 0) return null
  return { x: props.width, y: scaleY(props.points[n - 1]) }
})
</script>

<template>
  <svg
    v-if="points.length >= 2"
    :width="width"
    :height="height"
    :viewBox="`0 0 ${width} ${height}`"
    role="img"
    :aria-label="`最近趋势，当前 ${points[points.length - 1].toFixed(1)}`"
  >
    <path :d="path" fill="none" :stroke="color" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round" />
    <circle v-if="lastPoint" :cx="lastPoint.x" :cy="lastPoint.y" r="2" :fill="color" />
  </svg>
</template>
