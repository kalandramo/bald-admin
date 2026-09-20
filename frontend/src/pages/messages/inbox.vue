<script lang="ts" setup>
import type { Recipient } from "@@/apis/message/type"
import { deleteInboxApi, listInboxApi, markInboxReadApi } from "@@/apis/message"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import { formatDateTime } from "@@/utils/datetime"

defineOptions({ name: "MessageInbox" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认从收件箱删除该消息？" })

const loading = ref(false)
const list = ref<Recipient[]>([])

async function fetchList() {
  loading.value = true
  try {
    const res = await listInboxApi()
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleRead(row: Recipient) {
  markInboxReadApi(row.id).then(() => {
    ElMessage.success("已标记为已读")
    fetchList()
  })
}

function handleDeleteAction(row: Recipient) {
  handleDelete(() => deleteInboxApi(row.id), fetchList)
}

function statusTag(status?: string) {
  return status === "READ"
    ? { type: "info" as const, label: "已读" }
    : { type: "warning" as const, label: "未读" }
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="title" label="标题" />
        <el-table-column prop="content" label="内容" show-overflow-tooltip />
        <el-table-column label="状态" align="center" width="90">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status).type" size="small">
              {{ statusTag(row.status).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="接收时间" align="center" width="180">
          <template #default="{ row }">
            {{ row.created_at ? formatDateTime(row.created_at) : "—" }}
          </template>
        </el-table-column>
        <el-table-column label="操作" align="center" width="200">
          <template #default="{ row }">
            <el-button
              type="primary"
              link
              :disabled="row.status === 'READ'"
              @click="handleRead(row as Recipient)"
            >
              标为已读
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as Recipient)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<style lang="scss" scoped>
.app-container {
  padding: 20px;
}
</style>
