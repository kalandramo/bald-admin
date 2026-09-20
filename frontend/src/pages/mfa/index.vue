<script lang="ts" setup>
import type { EnrolledMethod, MFAStatus, StartEnrollResult } from "@@/apis/mfa/type"
import {
  confirmMFAEnrollApi,
  disableMFAApi,
  getMFAStatusApi,
  listMFAMethodsApi,
  revokeMFADeviceApi,
  startMFAEnrollApi
} from "@@/apis/mfa"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import { formatDateTime } from "@@/utils/datetime"

defineOptions({ name: "MFA" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认撤销该 MFA 设备？" })

const loading = ref(false)
const status = ref<MFAStatus>({ enabled: false, enforcement: "" })
const methods = ref<EnrolledMethod[]>([])

// 绑定向导
const enrollVisible = ref(false)
const enrolling = ref(false)
const enrollResult = ref<StartEnrollResult | null>(null)
const totpCode = ref("")
const display = ref("我的验证器")
const confirming = ref(false)

async function fetchData() {
  loading.value = true
  try {
    const [st, ms] = await Promise.all([getMFAStatusApi(), listMFAMethodsApi()])
    status.value = st
    methods.value = ms.items || []
  } finally {
    loading.value = false
  }
}

async function handleStartEnroll() {
  enrolling.value = true
  try {
    enrollResult.value = await startMFAEnrollApi()
    totpCode.value = ""
    enrollVisible.value = true
  } finally {
    enrolling.value = false
  }
}

function handleConfirmEnroll() {
  if (!enrollResult.value) return
  if (!totpCode.value) {
    ElMessage.warning("请输入验证器显示的验证码")
    return
  }
  confirming.value = true
  confirmMFAEnrollApi(enrollResult.value.operation_id, totpCode.value, display.value)
    .then(() => {
      ElMessage.success("绑定成功")
      enrollVisible.value = false
      fetchData()
    })
    .finally(() => (confirming.value = false))
}

function handleDisable(row: EnrolledMethod) {
  handleDelete(() => disableMFAApi(row.id), fetchData)
}

function handleRevoke(row: EnrolledMethod) {
  handleDelete(() => revokeMFADeviceApi(row.id), fetchData)
}

function enforcementLabel(v?: string) {
  const map: Record<string, string> = { NOT_REQUIRED: "不要求", OPTIONAL: "可选", REQUIRED: "强制" }
  return map[v ?? ""] || v || "—"
}

onMounted(fetchData)
</script>

<template>
  <div class="app-container">
    <el-card v-loading="loading" shadow="never" class="status-wrapper">
      <div class="status-row">
        <div>
          <span class="status-label">MFA 状态：</span>
          <el-tag :type="status.enabled ? 'success' : 'info'" size="small">
            {{ status.enabled ? "已启用" : "未启用" }}
          </el-tag>
          <span class="status-label enforcement">强制级别：</span>
          <el-tag size="small">
            {{ enforcementLabel(status.enforcement) }}
          </el-tag>
        </div>
        <el-button type="primary" :loading="enrolling" @click="handleStartEnroll">
          绑定新验证器
        </el-button>
      </div>
    </el-card>

    <el-card shadow="never">
      <el-table v-loading="loading" :data="methods" border stripe>
        <el-table-column prop="id" label="ID" />
        <el-table-column prop="method" label="方法" align="center" />
        <el-table-column prop="display" label="显示名" align="center" />
        <el-table-column label="状态" align="center" width="80">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
              {{ row.enabled ? "启用" : "禁用" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="绑定时间" align="center" width="180">
          <template #default="{ row }">
            {{ row.created_at ? formatDateTime(row.created_at) : "—" }}
          </template>
        </el-table-column>
        <el-table-column label="操作" align="center" width="200">
          <template #default="{ row }">
            <el-button type="warning" link @click="handleDisable(row as EnrolledMethod)">
              禁用
            </el-button>
            <el-button type="danger" link @click="handleRevoke(row as EnrolledMethod)">
              撤销
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="enrollVisible" title="绑定验证器" width="520px" :close-on-click-modal="false">
      <template v-if="enrollResult">
        <el-alert type="info" :closable="false" show-icon class="enroll-tip">
          <template #title>
            用验证器 App 扫描或手动输入密钥，然后填入生成的 6 位验证码
          </template>
        </el-alert>
        <el-form label-width="100px">
          <el-form-item label="OTP 链接">
            <el-input v-model="enrollResult.otp_auth_url" readonly />
          </el-form-item>
          <el-form-item label="密钥">
            <el-input v-model="enrollResult.secret" readonly />
          </el-form-item>
          <el-form-item label="显示名">
            <el-input v-model="display" />
          </el-form-item>
          <el-form-item label="验证码">
            <el-input v-model="totpCode" placeholder="6 位数字" maxlength="6" />
          </el-form-item>
        </el-form>
      </template>
      <template #footer>
        <el-button @click="enrollVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="confirming" @click="handleConfirmEnroll">
          确认绑定
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style lang="scss" scoped>
.app-container {
  padding: 20px;
}
.status-wrapper {
  margin-bottom: 20px;
}
.status-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.status-label {
  font-weight: 500;
}
.enforcement {
  margin-left: 16px;
}
.enroll-tip {
  margin-bottom: 16px;
}
</style>
