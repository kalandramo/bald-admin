<script lang="ts" setup>
import type { Tenant } from "@/api/generated/model"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import { formatDateTime } from "@@/utils/datetime"
import { TenantStatus } from "@/api/generated/model"
import { tenantServiceCreateTenant, tenantServiceDeleteTenant, tenantServiceListTenants, tenantServiceUpdateTenant } from "@/api/generated/tenant-service"

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该租户？" })

const loading = ref(false)
const list = ref<Tenant[]>([])
const dialogVisible = ref(false)
const dialogTitle = ref("")
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

function defaultForm(): Tenant {
  return {
    id: "",
    name: "",
    // protojson 输出枚举名（后端 writePB UseProtoNames，实测 "STATUS_ON"）
    status: TenantStatus.STATUS_UNSPECIFIED,
    remark: "",
    created_at: "",
    updated_at: ""
  }
}
const form = reactive<Tenant>(defaultForm())

const rules = {
  id: [{ required: true, message: "请输入租户编码", trigger: "blur" }],
  name: [{ required: true, message: "请输入租户名称", trigger: "blur" }]
}

async function fetchList() {
  loading.value = true
  try {
    const res = await tenantServiceListTenants()
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, defaultForm())
  dialogTitle.value = "新增租户"
  dialogVisible.value = true
}

function handleEdit(row: Tenant) {
  Object.assign(form, row)
  dialogTitle.value = "编辑租户"
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      if (form.created_at) {
        await tenantServiceUpdateTenant(form.id!, { name: form.name, status: form.status, remark: form.remark })
      } else {
        await tenantServiceCreateTenant({ id: form.id, name: form.name, remark: form.remark })
      }
      ElMessage.success("保存成功")
      dialogVisible.value = false
      fetchList()
    } finally {
      submitting.value = false
    }
  })
}

function handleDeleteAction(row: Tenant) {
  handleDelete(() => tenantServiceDeleteTenant(row.id!), fetchList)
}

function statusTag(status?: string) {
  const map: Record<string, string> = { STATUS_ON: "success", STATUS_OFF: "info", STATUS_FREEZE: "danger" }
  const labels: Record<string, string> = { STATUS_ON: "启用", STATUS_OFF: "禁用", STATUS_FREEZE: "冻结" }
  return { type: map[status ?? ""] || "info", label: labels[status ?? ""] || "未知" }
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleCreate">
        新增租户
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="id" label="租户编码" align="center" />
        <el-table-column prop="name" label="租户名称" align="center" />
        <el-table-column label="状态" align="center">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status).type as any">
              {{ statusTag(row.status).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="remark" label="备注" align="center" />
        <el-table-column label="创建时间" align="center" width="180">
          <template #default="{ row }">
            {{ formatDateTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row as Tenant)">
              编辑
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as Tenant)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="500px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
        <el-form-item label="租户编码" prop="id">
          <el-input v-model="form.id" placeholder="如 t-acme" :disabled="!!form.created_at" />
        </el-form-item>
        <el-form-item label="租户名称" prop="name">
          <el-input v-model="form.name" placeholder="请输入租户名称" />
        </el-form-item>
        <el-form-item label="状态" prop="status">
          <el-radio-group v-model="form.status">
            <el-radio :value="TenantStatus.STATUS_ON">
              启用
            </el-radio>
            <el-radio :value="TenantStatus.STATUS_OFF">
              禁用
            </el-radio>
            <el-radio :value="TenantStatus.STATUS_FREEZE">
              冻结
            </el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="form.remark" type="textarea" placeholder="请输入备注" />
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
