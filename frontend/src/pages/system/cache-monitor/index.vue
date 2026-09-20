<script lang="ts" setup>
import type { RedisCacheMonitorInfo } from "@/api/generated/model"
import { formatDateTime } from "@@/utils/datetime"
import { redisCacheMonitorServiceGetRedisCacheMonitor } from "@/api/generated/redis-cache-monitor-service"

defineOptions({ name: "CacheMonitor" })

const loading = ref(false)
const info = ref<RedisCacheMonitorInfo | null>(null)
const activeSection = ref("")

async function fetchData() {
  loading.value = true
  try {
    info.value = await redisCacheMonitorServiceGetRedisCacheMonitor()
    activeSection.value = info.value.sections?.[0]?.name || ""
  } finally {
    loading.value = false
  }
}

onMounted(fetchData)
</script>

<template>
  <div v-loading="loading" class="app-container">
    <el-card shadow="never" class="summary-wrapper">
      <div class="summary-row">
        <div class="summary-item">
          <span class="label">Redis 版本</span>
          <span class="value">{{ info?.sections?.find(s => s.name === "server")?.entries?.find(e => e.key === "redis_version")?.value || "—" }}</span>
        </div>
        <div class="summary-item">
          <span class="label">Key 总数</span>
          <span class="value">{{ info?.db_size ?? "—" }}</span>
        </div>
        <el-button type="primary" @click="fetchData">
          刷新
        </el-button>
      </div>
    </el-card>

    <el-card shadow="never" class="section-wrapper">
      <el-tabs v-model="activeSection">
        <el-tab-pane
          v-for="sec in info?.sections || []"
          :key="sec.name"
          :label="sec.name"
          :name="sec.name"
        >
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item
              v-for="entry in sec.entries || []"
              :key="entry.key"
              :label="entry.key"
            >
              {{ entry.value }}
            </el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <el-card shadow="never">
      <template #header>
        <span>慢日志</span>
      </template>
      <el-table :data="info?.slowlog || []" border stripe size="small">
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column label="时间" width="180">
          <template #default="{ row }">
            {{ row.created_at ? formatDateTime(row.created_at) : "—" }}
          </template>
        </el-table-column>
        <el-table-column prop="duration_usec" label="耗时(μs)" width="120" align="center" />
        <el-table-column prop="client_addr" label="客户端" width="160" align="center" />
        <el-table-column label="命令">
          <template #default="{ row }">
            {{ (row.args || []).join(" ") }}
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
.summary-wrapper {
  margin-bottom: 20px;
}
.summary-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.summary-item {
  margin-right: 32px;
}
.summary-item .label {
  color: var(--el-text-color-secondary);
  margin-right: 8px;
}
.summary-item .value {
  font-weight: 600;
}
.section-wrapper {
  margin-bottom: 20px;
}
</style>
