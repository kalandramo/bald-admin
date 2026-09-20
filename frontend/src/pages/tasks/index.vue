<script lang="ts" setup>
import type { Task } from "@@/apis/task/type"
import {
  controlTaskApi,
  deleteTaskApi,
  listTasksApi,
  restartAllTasksApi,
  startAllTasksApi,
  stopAllTasksApi
} from "@@/apis/task"
import { useConfirmAction } from "@@/composables/useConfirmAction"

defineOptions({ name: "Tasks" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该任务？" })

const loading = ref(false)
const list = ref<Task[]>([])

async function fetchList() {
  loading.value = true
  try {
    const res = await listTasksApi()
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleStartAll() {
  startAllTasksApi().then((r) => {
    ElMessage.success(`已启动 ${r.started} 个任务`)
    fetchList()
  })
}

function handleStopAll() {
  stopAllTasksApi().then(() => {
    ElMessage.success("已全部停止")
    fetchList()
  })
}

function handleRestartAll() {
  restartAllTasksApi().then((r) => {
    ElMessage.success(`已重启 ${r.restarted} 个任务`)
    fetchList()
  })
}

function handleControl(row: Task, start: boolean) {
  controlTaskApi(row.type_name, start).then(() => {
    ElMessage.success(start ? "已启动" : "已停止")
    fetchList()
  })
}

function handleDeleteAction(row: Task) {
  handleDelete(() => deleteTaskApi(row.type_name), fetchList)
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleStartAll">
        全部启动
      </el-button>
      <el-button type="warning" @click="handleStopAll">
        全部停止
      </el-button>
      <el-button type="info" @click="handleRestartAll">
        全部重启
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="type_name" label="任务类型" />
        <el-table-column prop="type" label="类型" align="center" />
        <el-table-column prop="cron_spec" label="Cron 表达式" align="center" />
        <el-table-column label="启用" align="center" width="80">
          <template #default="{ row }">
            <el-tag :type="row.enable ? 'success' : 'info'" size="small">
              {{ row.enable ? "启用" : "禁用" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="运行中" align="center" width="90">
          <template #default="{ row }">
            <el-tag :type="row.running ? 'success' : 'info'" size="small">
              {{ row.running ? "运行中" : "已停止" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="remark" label="备注" align="center" />
        <el-table-column label="操作" align="center" width="230">
          <template #default="{ row }">
            <el-button
              type="success"
              link
              :disabled="row.running"
              @click="handleControl(row as Task, true)"
            >
              启动
            </el-button>
            <el-button
              type="warning"
              link
              :disabled="!row.running"
              @click="handleControl(row as Task, false)"
            >
              停止
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as Task)">
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
.search-wrapper {
  margin-bottom: 20px;
}
</style>
