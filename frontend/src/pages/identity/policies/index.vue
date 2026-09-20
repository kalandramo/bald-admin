<script lang="ts" setup>
import type { LoginPolicy } from "@@/apis/identity/type"
import { countLoginPoliciesApi, createLoginPolicyApi, deleteLoginPolicyApi, listLoginPoliciesApi } from "@@/apis/identity"
import { useConfirmAction } from "@@/composables/useConfirmAction"

defineOptions({ name: "IdentityPolicies" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该登录策略？" })

const loading = ref(false)
const list = ref<LoginPolicy[]>([])
const total = ref(0)
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

const form = reactive({ target_id: "", type: "", method: "", value: "", reason: "" })
const rules = {
  type: [{ required: true, message: "请输入策略类型", trigger: "blur" }],
  method: [{ required: true, message: "请输入策略方法", trigger: "blur" }]
}

async function fetchList() {
  loading.value = true
  try {
    const [res, cnt] = await Promise.all([listLoginPoliciesApi(), countLoginPoliciesApi()])
    list.value = res.items || []
    total.value = cnt.total || 0
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, { target_id: "", type: "", method: "", value: "", reason: "" })
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      await createLoginPolicyApi({ ...form })
      ElMessage.success("保存成功")
      dialogVisible.value = false
      fetchList()
    } finally {
      submitting.value = false
    }
  })
}

function handleDeleteAction(row: LoginPolicy) {
  handleDelete(() => deleteLoginPolicyApi(row.id), fetchList)
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleCreate">
        新增登录策略
      </el-button>
      <span class="total-hint">共 {{ total }} 条策略</span>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="id" label="ID" />
        <el-table-column prop="target_id" label="目标ID" align="center" />
        <el-table-column prop="type" label="策略类型" align="center" />
        <el-table-column prop="method" label="方法" align="center" />
        <el-table-column prop="value" label="值" align="center" />
        <el-table-column prop="reason" label="原因" align="center" />
        <el-table-column label="操作" align="center" width="120">
          <template #default="{ row }">
            <el-button type="danger" link @click="handleDeleteAction(row as LoginPolicy)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" title="新增登录策略" width="520px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="目标ID" prop="target_id">
          <el-input v-model="form.target_id" placeholder="可空（全局策略）" />
        </el-form-item>
        <el-form-item label="策略类型" prop="type">
          <el-input v-model="form.type" placeholder="如 IP / TIME" />
        </el-form-item>
        <el-form-item label="方法" prop="method">
          <el-input v-model="form.method" placeholder="如 ALLOW / DENY" />
        </el-form-item>
        <el-form-item label="值" prop="value">
          <el-input v-model="form.value" />
        </el-form-item>
        <el-form-item label="原因" prop="reason">
          <el-input v-model="form.reason" type="textarea" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">
          确定
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style lang="scss" scoped>
.app-container {
  padding: 20px;
}
.search-wrapper {
  margin-bottom: 20px;
}
.total-hint {
  margin-left: 16px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
</style>
