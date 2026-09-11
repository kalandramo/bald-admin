<script lang="ts" setup>
import type { AuditRecord, AuditServiceListAuditRecordsParams } from "@/api/generated/model"
import { formatDateTime } from "@@/utils/datetime"
import { auditServiceGetAuditRecord, auditServiceListAuditRecords } from "@/api/generated/audit-service"

const loading = ref(false)
const list = ref<AuditRecord[]>([])
const nextPageToken = ref("")
const prevTokens = ref<string[]>([])
const drawerVisible = ref(false)
const currentRecord = ref<AuditRecord | null>(null)

const searchForm = reactive({
  category: "",
  subject: "",
  object: "",
  action: "",
  result: "",
  ip: ""
})

async function fetchList(token?: string) {
  loading.value = true
  try {
    const params: AuditServiceListAuditRecordsParams = {
      page_size: 20,
      page_token: token || undefined
    }
    if (searchForm.category) params.category = searchForm.category
    if (searchForm.subject) params.subject = searchForm.subject
    if (searchForm.object) params.object = searchForm.object
    if (searchForm.action) params.action = searchForm.action
    if (searchForm.result) params.result = searchForm.result
    if (searchForm.ip) params.ip = searchForm.ip

    const res = await auditServiceListAuditRecords(params)
    list.value = res.items || []
    nextPageToken.value = res.next_page_token || ""
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  prevTokens.value = []
  fetchList()
}

function handleReset() {
  Object.assign(searchForm, { category: "", subject: "", object: "", action: "", result: "", ip: "" })
  prevTokens.value = []
  fetchList()
}

function handleNextPage() {
  if (!nextPageToken.value) return
  prevTokens.value.push(nextPageToken.value)
  fetchList(nextPageToken.value)
}

function handlePrevPage() {
  if (prevTokens.value.length === 0) return
  prevTokens.value.pop()
  const token = prevTokens.value.length > 0 ? prevTokens.value[prevTokens.value.length - 1] : undefined
  fetchList(token)
}

async function handleViewDetail(row: AuditRecord) {
  try {
    const res = await auditServiceGetAuditRecord(row.id!)
    currentRecord.value = res.record ?? row
    drawerVisible.value = true
  } catch {
    currentRecord.value = row
    drawerVisible.value = true
  }
}

function resultTag(result?: string) {
  const map: Record<string, string> = { allow: "success", deny: "danger", error: "warning" }
  return map[result ?? ""] || "info"
}

function copyText(text: string | undefined) {
  navigator.clipboard.writeText(text ?? "").then(() => {
    ElMessage.success("已复制")
  })
}

onMounted(() => fetchList())
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-form :inline="true" :model="searchForm">
        <el-form-item label="分类">
          <el-select v-model="searchForm.category" placeholder="全部" clearable style="width: 120px">
            <el-option label="操作" value="operation" />
            <el-option label="登录" value="login" />
          </el-select>
        </el-form-item>
        <el-form-item label="主体">
          <el-input v-model="searchForm.subject" placeholder="操作主体" clearable />
        </el-form-item>
        <el-form-item label="资源对象">
          <el-input v-model="searchForm.object" placeholder="资源对象" clearable />
        </el-form-item>
        <el-form-item label="动作">
          <el-input v-model="searchForm.action" placeholder="动作" clearable />
        </el-form-item>
        <el-form-item label="结果">
          <el-select v-model="searchForm.result" placeholder="全部" clearable style="width: 100px">
            <el-option label="允许" value="allow" />
            <el-option label="拒绝" value="deny" />
            <el-option label="错误" value="error" />
          </el-select>
        </el-form-item>
        <el-form-item label="IP">
          <el-input v-model="searchForm.ip" placeholder="IP地址" clearable />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="handleSearch">
            查询
          </el-button>
          <el-button @click="handleReset">
            重置
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column label="时间" align="center" width="180">
          <template #default="{ row }">
            {{ formatDateTime(row.time) }}
          </template>
        </el-table-column>
        <el-table-column prop="category" label="分类" align="center" width="80" />
        <el-table-column prop="subject" label="主体" align="center" />
        <el-table-column prop="object" label="资源对象" align="center" />
        <el-table-column prop="action" label="动作" align="center" />
        <el-table-column label="结果" align="center" width="80">
          <template #default="{ row }">
            <el-tag :type="resultTag(row.result) as any" size="small">
              {{ row.result }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="ip_address" label="IP" align="center" width="130" />
        <el-table-column prop="user_agent" label="UA" align="center" width="200" show-overflow-tooltip />
        <el-table-column label="操作" align="center" width="100">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleViewDetail(row as AuditRecord)">
              详情
            </el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="pagination-wrapper">
        <el-button :disabled="prevTokens.length === 0" @click="handlePrevPage">
          上一页
        </el-button>
        <el-button :disabled="!nextPageToken" @click="handleNextPage">
          下一页
        </el-button>
      </div>
    </el-card>

    <!-- 详情抽屉 -->
    <el-drawer v-model="drawerVisible" title="审计详情" size="500px">
      <el-descriptions v-if="currentRecord" :column="1" border>
        <el-descriptions-item label="ID">
          {{ currentRecord.id }}
        </el-descriptions-item>
        <el-descriptions-item label="时间">
          {{ formatDateTime(currentRecord.time) }}
        </el-descriptions-item>
        <el-descriptions-item label="租户">
          {{ currentRecord.tenant_id }}
        </el-descriptions-item>
        <el-descriptions-item label="分类">
          {{ currentRecord.category }}
        </el-descriptions-item>
        <el-descriptions-item label="主体">
          {{ currentRecord.subject }}
        </el-descriptions-item>
        <el-descriptions-item label="资源对象">
          {{ currentRecord.object }}
        </el-descriptions-item>
        <el-descriptions-item label="动作">
          {{ currentRecord.action }}
        </el-descriptions-item>
        <el-descriptions-item label="结果">
          <el-tag :type="resultTag(currentRecord.result) as any">
            {{ currentRecord.result }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="错误信息">
          {{ currentRecord.error || "-" }}
        </el-descriptions-item>
        <el-descriptions-item label="IP">
          {{ currentRecord.ip_address }}
        </el-descriptions-item>
        <el-descriptions-item label="UA">
          {{ currentRecord.user_agent }}
        </el-descriptions-item>
        <el-descriptions-item label="Request ID">
          <span>{{ currentRecord.request_id }}</span>
          <el-button v-if="currentRecord.request_id" type="primary" link @click="copyText(currentRecord.request_id)">
            复制
          </el-button>
        </el-descriptions-item>
        <el-descriptions-item label="Trace ID">
          <span>{{ currentRecord.trace_id }}</span>
          <el-button v-if="currentRecord.trace_id" type="primary" link @click="copyText(currentRecord.trace_id)">
            复制
          </el-button>
        </el-descriptions-item>
      </el-descriptions>
    </el-drawer>
  </div>
</template>

<style lang="scss" scoped>
.app-container {
  padding: 20px;
}
.search-wrapper {
  margin-bottom: 20px;
}
.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
  gap: 10px;
}
</style>
