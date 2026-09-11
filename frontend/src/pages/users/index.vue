<script lang="ts" setup>
import type { User } from "@/api/generated/model"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import { formatDateTime } from "@@/utils/datetime"
import { userServiceCreateUser, userServiceDeleteUser, userServiceListUsers, userServiceUpdateUser } from "@/api/generated/user-service"

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该用户？" })

const loading = ref(false)
const list = ref<User[]>([])
const dialogVisible = ref(false)
const dialogTitle = ref("")
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

function defaultForm(): User & { password?: string } {
  return {
    id: "",
    username: "",
    roles: [],
    password: "",
    created_at: "",
    updated_at: ""
  }
}
const form = reactive<User & { password?: string }>(defaultForm())

const rules = {
  id: [{ required: true, message: "请输入用户ID", trigger: "blur" }],
  username: [
    { required: true, message: "请输入用户名", trigger: "blur" },
    // 与后端 usernamePattern 同规则双重拦截（后端为准）
    { pattern: /^\w{3,32}$/, message: "用户名须为 3-32 位英文字母、数字或下划线", trigger: "blur" }
  ],
  password: [{ required: true, message: "请输入密码", trigger: "blur" }]
}

async function fetchList() {
  loading.value = true
  try {
    const res = await userServiceListUsers()
    list.value = res.users || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, defaultForm())
  dialogTitle.value = "新增用户"
  dialogVisible.value = true
}

function handleEdit(row: User) {
  Object.assign(form, { ...row, password: "" })
  dialogTitle.value = "编辑用户"
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      if (form.created_at) {
        await userServiceUpdateUser(form.id!, {
          username: form.username,
          roles: form.roles,
          password: form.password || undefined
        })
      } else {
        await userServiceCreateUser({
          id: form.id,
          username: form.username,
          roles: form.roles,
          password: form.password!
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

function handleDeleteAction(row: User) {
  handleDelete(() => userServiceDeleteUser(row.id!), fetchList)
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleCreate">
        新增用户
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="id" label="用户ID" align="center" />
        <el-table-column prop="username" label="用户名" align="center" />
        <el-table-column label="角色" align="center">
          <template #default="{ row }">
            <el-tag v-for="role in row.roles" :key="role" size="small" class="role-tag">
              {{ role }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" align="center" width="180">
          <template #default="{ row }">
            {{ formatDateTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row as User)">
              编辑
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as User)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="500px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
        <el-form-item label="用户ID" prop="id">
          <el-input v-model="form.id" placeholder="如 u-admin" :disabled="!!form.created_at" />
        </el-form-item>
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" placeholder="请输入用户名" />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input v-model="form.password" type="password" :placeholder="form.created_at ? '留空则不修改' : '请输入密码'" show-password />
        </el-form-item>
        <el-form-item label="角色" prop="roles">
          <el-select v-model="form.roles" multiple placeholder="请选择角色">
            <el-option label="admin" value="admin" />
            <el-option label="viewer" value="viewer" />
          </el-select>
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
.role-tag {
  margin-right: 4px;
}
</style>
