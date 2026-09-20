<script lang="ts" setup>
import type { Position } from "@@/apis/org/type"
import { createPositionApi, deletePositionApi, listPositionsApi, updatePositionApi } from "@@/apis/org"
import { useConfirmAction } from "@@/composables/useConfirmAction"

defineOptions({ name: "OrgPositions" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该岗位？" })

const loading = ref(false)
const list = ref<Position[]>([])
const dialogVisible = ref(false)
const dialogTitle = ref("")
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

function defaultForm(): Position {
  return {
    id: "",
    name: "",
    code: "",
    headcount: 0,
    status: "ON",
    type: "",
    org_unit_id: "",
    reports_to_position_id: "",
    job_family: "",
    job_grade: "",
    level: 0,
    is_key_position: false,
    remark: ""
  }
}
const form = reactive<Position>(defaultForm())

const rules = {
  code: [{ required: true, message: "请输入岗位编码", trigger: "blur" }],
  name: [{ required: true, message: "请输入岗位名称", trigger: "blur" }]
}

async function fetchList() {
  loading.value = true
  try {
    const res = await listPositionsApi()
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, defaultForm())
  dialogTitle.value = "新增岗位"
  dialogVisible.value = true
}

function handleEdit(row: Position) {
  Object.assign(form, row)
  dialogTitle.value = "编辑岗位"
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      if (form.id) {
        await updatePositionApi(form.code, form)
      } else {
        await createPositionApi(form)
      }
      ElMessage.success("保存成功")
      dialogVisible.value = false
      fetchList()
    } finally {
      submitting.value = false
    }
  })
}

function handleDeleteAction(row: Position) {
  handleDelete(() => deletePositionApi(row.code), fetchList)
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
        新增岗位
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="name" label="岗位名称" />
        <el-table-column prop="code" label="编码" align="center" />
        <el-table-column prop="org_unit_name" label="所属组织" align="center" />
        <el-table-column prop="reports_to_position_name" label="汇报岗位" align="center" />
        <el-table-column prop="headcount" label="编制" align="center" width="70" />
        <el-table-column prop="level" label="层级" align="center" width="70" />
        <el-table-column label="关键岗位" align="center" width="90">
          <template #default="{ row }">
            <el-tag v-if="row.is_key_position" type="warning" size="small">
              是
            </el-tag>
            <span v-else>否</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" align="center" width="80">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status).type" size="small">
              {{ statusTag(row.status).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row as Position)">
              编辑
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as Position)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="600px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="110px">
        <el-form-item label="岗位编码" prop="code">
          <el-input v-model="form.code" placeholder="如 pos-backend" :disabled="!!form.id" />
        </el-form-item>
        <el-form-item label="岗位名称" prop="name">
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="所属组织编码" prop="org_unit_id">
          <el-input v-model="form.org_unit_id" placeholder="填组织 code" />
        </el-form-item>
        <el-form-item label="汇报岗位编码" prop="reports_to_position_id">
          <el-input v-model="form.reports_to_position_id" placeholder="填岗位 code，可空" />
        </el-form-item>
        <el-form-item label="类型" prop="type">
          <el-input v-model="form.type" />
        </el-form-item>
        <el-form-item label="编制" prop="headcount">
          <el-input-number v-model="form.headcount" :min="0" />
        </el-form-item>
        <el-form-item label="层级" prop="level">
          <el-input-number v-model="form.level" :min="0" />
        </el-form-item>
        <el-form-item label="职族" prop="job_family">
          <el-input v-model="form.job_family" />
        </el-form-item>
        <el-form-item label="职级" prop="job_grade">
          <el-input v-model="form.job_grade" />
        </el-form-item>
        <el-form-item label="关键岗位" prop="is_key_position">
          <el-switch v-model="form.is_key_position" />
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
