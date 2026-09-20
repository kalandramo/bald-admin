<script lang="ts" setup>
import type { PlanQuota } from "@@/apis/plan/type"
import { createPlanQuotaApi, deletePlanQuotaApi, listPlanQuotasApi, updatePlanQuotaApi } from "@@/apis/plan"
import { useConfirmAction } from "@@/composables/useConfirmAction"

defineOptions({ name: "PlanQuotas" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该套餐配额？" })

const loading = ref(false)
const list = ref<PlanQuota[]>([])
const planId = ref("")
const dialogVisible = ref(false)
const dialogTitle = ref("")
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

const form = reactive({ id: "", plan_id: "", quota_type: "", quota_value: 0 })

const rules = {
  plan_id: [{ required: true, message: "请输入套餐ID", trigger: "blur" }],
  quota_type: [{ required: true, message: "请输入配额类型", trigger: "blur" }]
}

async function fetchList() {
  if (!planId.value) {
    list.value = []
    return
  }
  loading.value = true
  try {
    const res = await listPlanQuotasApi(planId.value)
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, { id: "", plan_id: planId.value, quota_type: "", quota_value: 0 })
  dialogTitle.value = "新增套餐配额"
  dialogVisible.value = true
}

function handleEdit(row: PlanQuota) {
  Object.assign(form, row)
  dialogTitle.value = "编辑套餐配额"
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      if (form.id) {
        await updatePlanQuotaApi(form.id, form.quota_value)
      } else {
        await createPlanQuotaApi(form.plan_id, form.quota_type, form.quota_value)
      }
      ElMessage.success("保存成功")
      dialogVisible.value = false
      fetchList()
    } finally {
      submitting.value = false
    }
  })
}

function handleDeleteAction(row: PlanQuota) {
  handleDelete(() => deletePlanQuotaApi(row.id), fetchList)
}
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-form :inline="true">
        <el-form-item label="套餐ID">
          <el-input v-model="planId" placeholder="填套餐 id" style="width: 220px" @keyup.enter="fetchList" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="fetchList">
            查询
          </el-button>
          <el-button type="primary" @click="handleCreate">
            新增配额
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="id" label="ID" />
        <el-table-column prop="plan_id" label="套餐ID" align="center" />
        <el-table-column prop="quota_type" label="配额类型" align="center" />
        <el-table-column prop="quota_value" label="配额值" align="center" width="120" />
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row as PlanQuota)">
              编辑
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as PlanQuota)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="500px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="套餐ID" prop="plan_id">
          <el-input v-model="form.plan_id" :disabled="!!form.id" />
        </el-form-item>
        <el-form-item label="配额类型" prop="quota_type">
          <el-input v-model="form.quota_type" placeholder="如 max_users" :disabled="!!form.id" />
        </el-form-item>
        <el-form-item label="配额值" prop="quota_value">
          <el-input-number v-model="form.quota_value" :min="0" />
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
</style>
