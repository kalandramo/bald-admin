<script lang="ts" setup>
import type { Permission, RolePolicy } from "@/api/generated/model"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import { formatDateTime } from "@@/utils/datetime"
import {
  permissionServiceCreatePermission,
  permissionServiceCreateRolePolicy,
  permissionServiceDeletePermission,
  permissionServiceDeleteRolePolicy,
  permissionServiceListPermissions,
  permissionServiceListRolePolicies,
  permissionServiceUpdatePermission
} from "@/api/generated/permission-service"

const { handleDelete } = useConfirmAction()

const activeTab = ref("permissions")
const permLoading = ref(false)
const policyLoading = ref(false)
const permList = ref<Permission[]>([])
const policyList = ref<RolePolicy[]>([])

// 权限点弹窗
const permDialogVisible = ref(false)
const permDialogTitle = ref("")
const permSubmitting = ref(false)
const permFormRef = useTemplateRef("permFormRef")
const permForm = reactive<Permission>({ id: "", name: "", menu_ids: [], remark: "", created_at: "", updated_at: "" })
const permRules = {
  id: [{ required: true, message: "请输入权限码", trigger: "blur" }],
  name: [{ required: true, message: "请输入权限名称", trigger: "blur" }]
}

// 策略弹窗
const policyDialogVisible = ref(false)
const policySubmitting = ref(false)
const policyFormRef = useTemplateRef("policyFormRef")
const policyForm = reactive<RolePolicy>({ id: "", role: "", object: "", action: "" })
const policyRules = {
  role: [{ required: true, message: "请输入角色", trigger: "blur" }],
  object: [{ required: true, message: "请输入资源对象", trigger: "blur" }],
  action: [{ required: true, message: "请输入动作", trigger: "blur" }]
}

async function fetchPermissions() {
  permLoading.value = true
  try {
    const res = await permissionServiceListPermissions()
    permList.value = res.items || []
  } finally {
    permLoading.value = false
  }
}

async function fetchPolicies() {
  policyLoading.value = true
  try {
    const res = await permissionServiceListRolePolicies()
    policyList.value = res.items || []
  } finally {
    policyLoading.value = false
  }
}

function handleTabChange(name: string | number) {
  if (name === "policies" && policyList.value.length === 0) {
    fetchPolicies()
  }
}

// 权限点操作
function handleCreatePerm() {
  Object.assign(permForm, { id: "", name: "", menu_ids: [], remark: "", created_at: "", updated_at: "" })
  permDialogTitle.value = "新增权限点"
  permDialogVisible.value = true
}

function handleEditPerm(row: Permission) {
  Object.assign(permForm, row)
  permDialogTitle.value = "编辑权限点"
  permDialogVisible.value = true
}

function handleSubmitPerm() {
  permFormRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    permSubmitting.value = true
    try {
      if (permForm.created_at) {
        await permissionServiceUpdatePermission(permForm.id!, {
          name: permForm.name,
          menu_ids: permForm.menu_ids,
          menu_ids_set: true,
          remark: permForm.remark
        })
      } else {
        await permissionServiceCreatePermission({
          id: permForm.id,
          name: permForm.name,
          menu_ids: permForm.menu_ids,
          remark: permForm.remark
        })
      }
      ElMessage.success("保存成功")
      permDialogVisible.value = false
      fetchPermissions()
    } finally {
      permSubmitting.value = false
    }
  })
}

function handleDeletePerm(row: Permission) {
  handleDelete(() => permissionServiceDeletePermission(row.id!), fetchPermissions)
}

// 策略操作
function handleCreatePolicy() {
  Object.assign(policyForm, { id: "", role: "", object: "", action: "" })
  policyDialogVisible.value = true
}

function handleSubmitPolicy() {
  policyFormRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    policySubmitting.value = true
    try {
      await permissionServiceCreateRolePolicy({
        role: policyForm.role,
        object: policyForm.object,
        action: policyForm.action
      })
      ElMessage.success("保存成功")
      policyDialogVisible.value = false
      fetchPolicies()
    } finally {
      policySubmitting.value = false
    }
  })
}

function handleDeletePolicy(row: RolePolicy) {
  handleDelete(() => permissionServiceDeleteRolePolicy(row.id!), fetchPolicies)
}

onMounted(fetchPermissions)
</script>

<template>
  <div class="app-container">
    <el-tabs v-model="activeTab" @tab-change="handleTabChange">
      <el-tab-pane label="权限点" name="permissions">
        <el-card shadow="never" class="search-wrapper">
          <el-button type="primary" @click="handleCreatePerm">
            新增权限点
          </el-button>
        </el-card>
        <el-card shadow="never">
          <el-table v-loading="permLoading" :data="permList" border stripe>
            <el-table-column prop="id" label="权限码" align="center" />
            <el-table-column prop="name" label="权限名称" align="center" />
            <el-table-column prop="remark" label="备注" align="center" />
            <el-table-column label="创建时间" align="center" width="180">
              <template #default="{ row }">
                {{ formatDateTime(row.created_at) }}
              </template>
            </el-table-column>
            <el-table-column label="操作" align="center" width="180">
              <template #default="{ row }">
                <el-button type="primary" link @click="handleEditPerm(row as Permission)">
                  编辑
                </el-button>
                <el-button type="danger" link @click="handleDeletePerm(row as Permission)">
                  删除
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="角色策略" name="policies">
        <el-card shadow="never" class="search-wrapper">
          <el-button type="primary" @click="handleCreatePolicy">
            新增策略
          </el-button>
        </el-card>
        <el-card shadow="never">
          <el-table v-loading="policyLoading" :data="policyList" border stripe>
            <el-table-column prop="role" label="角色" align="center" />
            <el-table-column prop="object" label="资源对象" align="center" />
            <el-table-column prop="action" label="动作" align="center" />
            <el-table-column label="操作" align="center" width="100">
              <template #default="{ row }">
                <el-button type="danger" link @click="handleDeletePolicy(row as RolePolicy)">
                  删除
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <!-- 权限点弹窗 -->
    <el-dialog v-model="permDialogVisible" :title="permDialogTitle" width="500px" :close-on-click-modal="false">
      <el-form ref="permFormRef" :model="permForm" :rules="permRules" label-width="100px">
        <el-form-item label="权限码" prop="id">
          <el-input v-model="permForm.id" placeholder="如 tenant:list" :disabled="!!permForm.created_at" />
        </el-form-item>
        <el-form-item label="权限名称" prop="name">
          <el-input v-model="permForm.name" placeholder="如 列出租户" />
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="permForm.remark" type="textarea" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="permDialogVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="permSubmitting" @click="handleSubmitPerm">
          确定
        </el-button>
      </template>
    </el-dialog>

    <!-- 策略弹窗 -->
    <el-dialog v-model="policyDialogVisible" title="新增策略" width="500px" :close-on-click-modal="false">
      <el-form ref="policyFormRef" :model="policyForm" :rules="policyRules" label-width="100px">
        <el-form-item label="角色" prop="role">
          <el-select v-model="policyForm.role" placeholder="请选择角色">
            <el-option label="admin" value="admin" />
            <el-option label="viewer" value="viewer" />
          </el-select>
        </el-form-item>
        <el-form-item label="资源对象" prop="object">
          <el-input v-model="policyForm.object" placeholder="如 tenant" />
        </el-form-item>
        <el-form-item label="动作" prop="action">
          <el-input v-model="policyForm.action" placeholder="如 get/list/write/delete" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="policyDialogVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="policySubmitting" @click="handleSubmitPolicy">
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
