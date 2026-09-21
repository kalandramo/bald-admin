<script lang="ts" setup>
import type { OrgUnit } from "@@/apis/org/type"
import { OrgUnitStatus, OrgUnitType } from "@@/apis/org/type"
import { createOrgUnitApi, deleteOrgUnitApi, listOrgUnitsApi, updateOrgUnitApi } from "@@/apis/org"
import { useConfirmAction } from "@@/composables/useConfirmAction"

defineOptions({ name: "OrgUnits" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该组织单元？" })

const loading = ref(false)
const tree = ref<OrgUnit[]>([])
const dialogVisible = ref(false)
const dialogTitle = ref("")
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

// 枚举选项（值即 protojson 输出的符号名，见 api/protos/identity/v1/org_unit.proto）。
const typeOptions = Object.entries(OrgUnitType)
  .filter(([k]) => k !== "TYPE_UNSPECIFIED")
  .map(([k, v]) => ({ label: k, value: v }))

function defaultForm(): OrgUnit {
  return {
    id: "",
    name: "",
    code: "",
    type: OrgUnitType.TYPE_DEPARTMENT,
    parent_id: "",
    status: OrgUnitStatus.STATUS_ON,
    sort_order: 0,
    leader_id: "",
    remark: "",
    description: ""
  }
}
const form = reactive<OrgUnit>(defaultForm())

const rules = {
  code: [{ required: true, message: "请输入组织编码", trigger: "blur" }],
  name: [{ required: true, message: "请输入组织名称", trigger: "blur" }]
}

async function fetchList() {
  loading.value = true
  try {
    const res = await listOrgUnitsApi()
    tree.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, defaultForm())
  dialogTitle.value = "新增组织单元"
  dialogVisible.value = true
}

function handleEdit(row: OrgUnit) {
  Object.assign(form, { ...row, children: undefined })
  dialogTitle.value = "编辑组织单元"
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      if (form.id) {
        await updateOrgUnitApi(form.code!, {
          code: form.code,
          name: form.name,
          type: form.type,
          parent_id: form.parent_id,
          status: form.status,
          sort_order: form.sort_order,
          sort_order_set: true,
          leader_id: form.leader_id,
          remark: form.remark,
          description: form.description
        })
      } else {
        await createOrgUnitApi({
          code: form.code,
          name: form.name,
          type: form.type,
          parent_id: form.parent_id,
          status: form.status,
          sort_order: form.sort_order,
          leader_id: form.leader_id,
          remark: form.remark,
          description: form.description
        })
      }
      ElMessage.success("保存成功")
      dialogVisible.value = false
      fetchList()
    } finally {
      submitting.value = false
    }
  })
}

function handleDeleteAction(row: OrgUnit) {
  handleDelete(() => deleteOrgUnitApi(row.code!), fetchList)
}

function statusTag(status?: string) {
  return status === OrgUnitStatus.STATUS_ON
    ? { type: "success" as const, label: "启用" }
    : { type: "info" as const, label: "禁用" }
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleCreate">
        新增组织单元
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table
        v-loading="loading"
        :data="tree"
        row-key="id"
        border
        stripe
        default-expand-all
        :tree-props="{ children: 'children' }"
      >
        <el-table-column prop="name" label="组织名称" />
        <el-table-column prop="code" label="编码" align="center" />
        <el-table-column prop="type" label="类型" align="center" width="120" />
        <el-table-column prop="leader_name" label="负责人" align="center" />
        <el-table-column label="状态" align="center" width="80">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status).type" size="small">
              {{ statusTag(row.status).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="sort_order" label="排序" align="center" width="70" />
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row as OrgUnit)">
              编辑
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as OrgUnit)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="550px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
        <el-form-item label="组织编码" prop="code">
          <el-input v-model="form.code" placeholder="如 dept-rd" :disabled="!!form.id" />
        </el-form-item>
        <el-form-item label="组织名称" prop="name">
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="类型" prop="type">
          <el-select v-model="form.type" placeholder="选择组织类型">
            <el-option v-for="opt in typeOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="父级编码" prop="parent_id">
          <el-input v-model="form.parent_id" placeholder="留空为根节点" />
        </el-form-item>
        <el-form-item label="负责人ID" prop="leader_id">
          <el-input v-model="form.leader_id" />
        </el-form-item>
        <el-form-item label="状态" prop="status">
          <el-radio-group v-model="form.status">
            <el-radio :value="OrgUnitStatus.STATUS_ON">
              启用
            </el-radio>
            <el-radio :value="OrgUnitStatus.STATUS_OFF">
              禁用
            </el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="排序" prop="sort_order">
          <el-input-number v-model="form.sort_order" :min="0" />
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
