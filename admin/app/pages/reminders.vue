<script setup lang="ts">
import { reminderUnread, type MyReminder } from '~/composables/useMyReminders'

// 我的提醒：门店 AI 写给本人的话，一件事一条，后续接在下面。事情办完 AI 自己撤掉，这里只管「知道了」。
definePageMeta({ title: '我的提醒', width: 'narrow', description: '门店 AI 发现要你处理的事写在这里，事情办完会自己撤掉。', art: 'bell' })

const { can } = useMe()
const { items, loaded, loadError, marking, load, markRead } = useMyReminders()

onMounted(() => load())

// 这件事在哪个页面办：团购券在收银页关联单据。
function subjectLink(item: MyReminder) {
  if (item.subject_kind === 'coupon_redemption' && can('order.create') && can('groupbuy.redeem')) return { to: '/cashier', label: '去收银关联' }
  return null
}
</script>

<template>
  <div>
    <ListSkeleton v-if="!loaded && !loadError" />
    <LoadError v-else-if="!loaded" title="提醒没有读出来" :hint="loadError" @retry="load()" />
    <EmptyState v-else-if="!items.length" title="没有提醒" hint="门店 AI 看到要你处理的事，会写在这里。" />

    <ul v-else class="divide-y divide-default border-y border-default" data-testid="my-reminders">
      <li v-for="item in items" :key="item.id" class="py-3" :data-reminder="item.id">
        <p class="flex flex-wrap items-center gap-x-2 text-meta text-muted tabular-nums">
          <span v-if="item.tier === 'now'" class="tag bg-tag-yellow">要马上处理</span>
          <span>{{ formatStamp(item.sent_at) }}</span>
          <span v-if="reminderUnread(item)" class="font-semibold text-default">没看过</span>
        </p>
        <p class="mt-1 text-sm text-pretty" :class="reminderUnread(item) ? 'font-semibold' : ''">{{ item.body }}</p>
        <ol v-if="item.replies.length" class="mt-2 space-y-2 border-s-2 border-default ps-3">
          <li v-for="reply in item.replies" :key="reply.id" class="text-sm">
            <span class="block text-meta text-muted tabular-nums">{{ formatStamp(reply.sent_at) }}</span>
            <span class="text-pretty" :class="reply.read_at ? '' : 'font-semibold'">{{ reply.body }}</span>
          </li>
        </ol>
        <div v-if="subjectLink(item) || reminderUnread(item)" class="mt-2 flex gap-1">
          <UButton v-if="subjectLink(item)" :to="subjectLink(item)!.to" size="sm" color="neutral" variant="outline">{{ subjectLink(item)!.label }}</UButton>
          <UButton
            v-if="reminderUnread(item)"
            size="sm"
            color="neutral"
            variant="ghost"
            :loading="marking[item.id]"
            @click="markRead(item.id)"
          >
            知道了
          </UButton>
        </div>
      </li>
    </ul>
  </div>
</template>
