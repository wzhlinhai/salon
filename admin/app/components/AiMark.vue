<script setup lang="ts">
// AI 的形象（DESIGN.md 图标）：一笔画的小脸，头顶一缕卷发刘海，是店里的「助手」；两团淡粉腮红，
// 色块取自侧栏图标、空状态插画同一套标签色。不用闪光符号。只做装饰，读屏跳过，旁边的按钮或标题写明是 AI。
</script>

<template>
  <svg viewBox="0 0 32 32" fill="none" stroke="var(--color-ink)" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
    <ellipse cx="9.6" cy="20.6" rx="2.4" ry="1.6" fill="var(--color-tag-pink)" stroke="none" style="filter: saturate(2.2) brightness(0.95)" />
    <ellipse cx="22.4" cy="20.6" rx="2.4" ry="1.6" fill="var(--color-tag-pink)" stroke="none" style="filter: saturate(2.2) brightness(0.95)" />
    <path d="M8 15 C7.5 8 13 5 18 5.8 C23 6.6 25.6 10.2 24.4 13.4 C23.6 15.6 20.8 15.2 21 13" />
    <circle cx="12.6" cy="17.6" r="1.3" fill="var(--color-ink)" stroke="none" />
    <circle cx="19.4" cy="17.6" r="1.3" fill="var(--color-ink)" stroke="none" />
    <path d="M13.2 22.2 Q16 24.6 18.8 22.2" />
  </svg>
</template>
