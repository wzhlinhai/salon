<script setup lang="ts">
// 空状态的小插画（DESIGN.md 空状态）：墨色细线加一块低饱和色块，照 Notion 页内插画的画法；只做装饰，读屏跳过。
// 画的是店里的东西，让空页面看得出是一家美发店的系统，而不是通用表格。
export type SpotArtName = 'scissors' | 'chair' | 'receipt' | 'chart' | 'card' | 'calendar'
defineProps<{ name: SpotArtName }>()
</script>

<template>
  <svg
    viewBox="0 0 120 96"
    fill="none"
    stroke="var(--color-ink)"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
    data-testid="spot-art"
  >
    <template v-if="name === 'scissors'">
      <circle cx="66" cy="46" r="30" fill="var(--color-tag-pink)" stroke="none" />
      <path d="M54.3 69.7 L58.2 50.9 L73 13 L65.8 53.1 L59.1 71.1 Z" fill="#fff" />
      <path d="M42.9 54.9 L60.9 48.2 L101 41 L63.1 55.8 L44.3 59.7 Z" fill="#fff" />
      <circle cx="62" cy="52" r="1.8" fill="var(--color-ink)" />
      <circle cx="34" cy="60" r="10" fill="#fff" />
      <circle cx="54" cy="80" r="10" fill="#fff" />
      <path d="M74 86 h34" />
      <path d="M78 86 v-5 M83 86 v-5 M88 86 v-5 M93 86 v-5 M98 86 v-5 M103 86 v-5" />
    </template>

    <template v-else-if="name === 'chair'">
      <circle cx="62" cy="44" r="30" fill="var(--color-tag-yellow)" stroke="none" />
      <rect x="74" y="12" width="12" height="8" rx="3" fill="#fff" />
      <rect x="73" y="22" width="14" height="36" rx="4" fill="#fff" />
      <rect x="36" y="50" width="50" height="10" rx="4" fill="#fff" />
      <path d="M44 50 v-8 h26" />
      <path d="M40 60 L32 74 h14" />
      <path d="M62 60 v16" />
      <ellipse cx="62" cy="80" rx="18" ry="4" fill="#fff" />
    </template>

    <template v-else-if="name === 'receipt'">
      <circle cx="60" cy="46" r="30" fill="var(--color-tag-green)" stroke="none" />
      <path d="M38 12 H82 V78 l-5.5 5 -5.5 -5 -5.5 5 -5.5 -5 -5.5 5 -5.5 -5 -5.5 5 -5.5 -5 Z" fill="#fff" />
      <path d="M46 26 h28 M46 36 h18 M46 46 h22" />
      <path d="M46 60 h28" stroke-dasharray="2 3" />
      <path d="M62 68 h12" stroke-width="2.4" />
    </template>

    <template v-else-if="name === 'chart'">
      <circle cx="60" cy="46" r="30" fill="var(--color-tag-blue)" stroke="none" />
      <rect x="32" y="58" width="10" height="20" rx="2" fill="#fff" />
      <rect x="48" y="44" width="10" height="34" rx="2" fill="#fff" />
      <rect x="64" y="52" width="10" height="26" rx="2" fill="#fff" />
      <rect x="80" y="34" width="10" height="44" rx="2" fill="#fff" />
      <path d="M24 78 h74" />
      <path d="M90 14 v8 M86 18 h8" />
    </template>

    <template v-else-if="name === 'card'">
      <circle cx="60" cy="46" r="30" fill="var(--color-tag-purple)" stroke="none" />
      <rect x="34" y="22" width="56" height="36" rx="6" fill="#fff" transform="rotate(-10 62 40)" />
      <rect x="28" y="36" width="56" height="36" rx="6" fill="#fff" />
      <path d="M28 46 h56" stroke-width="5" stroke="var(--color-ink)" />
      <path d="M36 62 h18 M72 62 h4" />
    </template>

    <template v-else>
      <circle cx="60" cy="46" r="30" fill="var(--color-tag-orange)" stroke="none" />
      <rect x="32" y="20" width="56" height="58" rx="6" fill="#fff" />
      <path d="M32 34 h56" />
      <path d="M46 14 v12 M74 14 v12" />
      <rect x="64" y="56" width="10" height="9" rx="2" fill="var(--color-tag-orange)" />
      <path d="M42 46 h.01 M53 46 h.01 M64 46 h.01 M75 46 h.01 M42 60 h.01 M53 60 h.01" stroke-width="3" />
    </template>
  </svg>
</template>
