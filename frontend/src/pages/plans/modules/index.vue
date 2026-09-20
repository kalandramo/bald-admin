<script lang="ts" setup>
import type { PlanModule } from "@@/apis/plan/type"
import { createPlanModuleApi, deletePlanModuleApi, listPlanModulesApi, updatePlanModuleApi } from "@@/apis/plan"
import { useConfirmAction } from "@@/composables/useConfirmAction"

defineOptions({ name: "PlanModules" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该套餐模块？" })

const loading = ref(false)
const list = ref<PlanModule[]>([])
const planId = ref("")
const dialogVisible = ref(false)
const dialogTitle = ref("")
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

const form = reactive({ id: "", plan_id: "", module: "" })

const rules = {
  plan_id: [{ required: true, message: "请输入套餐ID", trigger: "blur" }],
  module: [{ required: true, message: "请输入模块名", trigger: "blur" }]
}

async function fetchList() {
  if (!planId.value) {
    list.value = []
    return
  }
  loading.value = true
  try {
    const res = await listPlanModulesApi(planId.value)
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, { id: "", plan_id: planId.value, module: "" })
  dialogTitle.value = "新增套餐模块"
  dialogVisible.value = true
}

function handleEdit(row: PlanModule) {
  Object.assign(form, row)
  dialogTitle.value = "编辑套餐模块"
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      if (form.id) {
        await updatePlanModuleApi(form.id, form.module)
      } else {
        await createPlanModuleApi(form.plan_id, form.module)
      }
      ElMessage.success("保存成功")
      dialogVisible.value = false
      fetchList()
    } finally {
      submitting.value = false
    }
  })
}

function handleDeleteAction(row: PlanModule) {
  handleDelete(() => deletePlanModuleApi(row.id), fetchList)
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
            新增模块
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="id" label="ID" />
        <el-table-column prop="plan_id" label="套餐ID" align="center" />
        <el-table-column prop="module" label="模块名" align="center" />
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row as PlanModule)">
              编辑
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as PlanModule)">
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
        <el-form-item label="模块名" prop="module">
          <el-input v-model="form.module" placeholder="如 user / tenant" />
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
