<script lang="ts" setup>
import type { CreatePlanRequest, Plan } from "@@/apis/plan/type"
import { createPlanApi, deletePlanApi, listPlansApi, updatePlanApi } from "@@/apis/plan"
import { useConfirmAction } from "@@/composables/useConfirmAction"

defineOptions({ name: "Plans" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该套餐？其下模块与配额将一并级联删除" })

const loading = ref(false)
const list = ref<Plan[]>([])
const dialogVisible = ref(false)
const dialogTitle = ref("")
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

function defaultForm(): CreatePlanRequest {
  return {
    code: "",
    name: "",
    version: "",
    expiry_policy: "",
    data_retention_days: 0,
    status: "ON",
    remark: ""
  }
}
const form = reactive<CreatePlanRequest>(defaultForm())
const editingId = ref("")

const rules = {
  code: [{ required: true, message: "请输入套餐编码", trigger: "blur" }],
  name: [{ required: true, message: "请输入套餐名称", trigger: "blur" }]
}

async function fetchList() {
  loading.value = true
  try {
    const res = await listPlansApi()
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, defaultForm())
  editingId.value = ""
  dialogTitle.value = "新增套餐"
  dialogVisible.value = true
}

function handleEdit(row: Plan) {
  Object.assign(form, { ...row, code: row.id })
  editingId.value = row.id
  dialogTitle.value = "编辑套餐"
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      if (editingId.value) {
        await updatePlanApi(editingId.value, {
          id: editingId.value,
          name: form.name,
          version: form.version,
          expiry_policy: form.expiry_policy,
          data_retention_days: form.data_retention_days,
          status: form.status,
          remark: form.remark
        })
      } else {
        await createPlanApi(form)
      }
      ElMessage.success("保存成功")
      dialogVisible.value = false
      fetchList()
    } finally {
      submitting.value = false
    }
  })
}

function handleDeleteAction(row: Plan) {
  handleDelete(() => deletePlanApi(row.id), fetchList)
}

function statusTag(status?: string) {
  return status === "ON"
    ? { type: "success" as const, label: "启用" }
    : { type: "info" as const, label: "禁用" }
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleCreate">
        新增套餐
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="id" label="套餐编码" align="center" />
        <el-table-column prop="name" label="套餐名称" />
        <el-table-column prop="version" label="版本" align="center" width="100" />
        <el-table-column prop="data_retention_days" label="数据保留(天)" align="center" width="120" />
        <el-table-column label="状态" align="center" width="80">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status).type" size="small">
              {{ statusTag(row.status).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="remark" label="备注" align="center" />
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row as Plan)">
              编辑
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as Plan)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="550px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="110px">
        <el-form-item label="套餐编码" prop="code">
          <el-input v-model="form.code" placeholder="如 plan-basic" :disabled="!!editingId" />
        </el-form-item>
        <el-form-item label="套餐名称" prop="name">
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="版本" prop="version">
          <el-input v-model="form.version" />
        </el-form-item>
        <el-form-item label="过期策略" prop="expiry_policy">
          <el-input v-model="form.expiry_policy" placeholder="如 FREEZE / DOWNGRADE" />
        </el-form-item>
        <el-form-item label="数据保留(天)" prop="data_retention_days">
          <el-input-number v-model="form.data_retention_days" :min="0" />
        </el-form-item>
        <el-form-item label="状态" prop="status">
          <el-radio-group v-model="form.status">
            <el-radio value="ON">
              启用
            </el-radio>
            <el-radio value="OFF">
              禁用
            </el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="form.remark" type="textarea" />
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
