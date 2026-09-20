<script lang="ts" setup>
import type { Credential } from "@@/apis/identity/type"
import {
  changeCredentialApi,
  createCredentialApi,
  deleteCredentialApi,
  listCredentialsApi,
  resetCredentialApi
} from "@@/apis/identity"
import { useConfirmAction } from "@@/composables/useConfirmAction"

defineOptions({ name: "IdentityCredentials" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该凭证？" })

const loading = ref(false)
const list = ref<Credential[]>([])
const filterUserId = ref("")

// 新增
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = useTemplateRef("formRef")
const form = reactive({
  user_id: "",
  identity_type: "USERNAME",
  identifier: "",
  credential_type: "PASSWORD",
  password: ""
})
const rules = {
  user_id: [{ required: true, message: "请输入用户ID", trigger: "blur" }],
  identifier: [{ required: true, message: "请输入标识（如用户名）", trigger: "blur" }],
  password: [{ required: true, message: "请输入密码", trigger: "blur" }]
}

// 改密/重置弹窗
const pwdDialogVisible = ref(false)
const pwdMode = ref<"change" | "reset">("change")
const pwdForm = reactive({ identity_type: "USERNAME", identifier: "", old_password: "", new_password: "" })

async function fetchList() {
  loading.value = true
  try {
    const res = await listCredentialsApi(filterUserId.value)
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, { user_id: "", identity_type: "USERNAME", identifier: "", credential_type: "PASSWORD", password: "" })
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      await createCredentialApi({ ...form })
      ElMessage.success("保存成功")
      dialogVisible.value = false
      fetchList()
    } finally {
      submitting.value = false
    }
  })
}

function handleDeleteAction(row: Credential) {
  handleDelete(() => deleteCredentialApi(row.id), fetchList)
}

function openChange(row: Credential) {
  pwdMode.value = "change"
  Object.assign(pwdForm, { identity_type: row.identity_type, identifier: row.identifier, old_password: "", new_password: "" })
  pwdDialogVisible.value = true
}

function openReset(row: Credential) {
  pwdMode.value = "reset"
  Object.assign(pwdForm, { identity_type: row.identity_type, identifier: row.identifier, old_password: "", new_password: "" })
  pwdDialogVisible.value = true
}

function submitPwd() {
  submitting.value = true
  const done = () => {
    ElMessage.success("操作成功")
    pwdDialogVisible.value = false
  }
  const p = pwdMode.value === "change"
    ? changeCredentialApi({
        identity_type: pwdForm.identity_type,
        identifier: pwdForm.identifier,
        old_password: pwdForm.old_password,
        new_password: pwdForm.new_password
      })
    : resetCredentialApi({
        identity_type: pwdForm.identity_type,
        identifier: pwdForm.identifier,
        new_password: pwdForm.new_password
      })
  p.then(done).finally(() => (submitting.value = false))
}
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-form :inline="true">
        <el-form-item label="用户ID">
          <el-input v-model="filterUserId" placeholder="按 user_id 过滤，可空" style="width: 220px" @keyup.enter="fetchList" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="fetchList">
            查询
          </el-button>
          <el-button type="primary" @click="handleCreate">
            新增凭证
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="id" label="ID" />
        <el-table-column prop="user_id" label="用户ID" align="center" />
        <el-table-column prop="identity_type" label="身份类型" align="center" />
        <el-table-column prop="identifier" label="标识" align="center" />
        <el-table-column prop="credential_type" label="凭证类型" align="center" />
        <el-table-column prop="status" label="状态" align="center" width="90" />
        <el-table-column label="操作" align="center" width="260">
          <template #default="{ row }">
            <el-button type="primary" link @click="openChange(row as Credential)">
              改密
            </el-button>
            <el-button type="warning" link @click="openReset(row as Credential)">
              重置
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as Credential)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" title="新增凭证" width="520px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
        <el-form-item label="用户ID" prop="user_id">
          <el-input v-model="form.user_id" placeholder="如 u-admin" />
        </el-form-item>
        <el-form-item label="身份类型" prop="identity_type">
          <el-input v-model="form.identity_type" placeholder="如 USERNAME / EMAIL" />
        </el-form-item>
        <el-form-item label="标识" prop="identifier">
          <el-input v-model="form.identifier" placeholder="如 admin" />
        </el-form-item>
        <el-form-item label="凭证类型" prop="credential_type">
          <el-input v-model="form.credential_type" placeholder="如 PASSWORD" />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input v-model="form.password" type="password" show-password />
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

    <el-dialog v-model="pwdDialogVisible" :title="pwdMode === 'change' ? '修改密码' : '重置密码'" width="480px" :close-on-click-modal="false">
      <el-form :model="pwdForm" label-width="100px">
        <el-form-item label="身份类型">
          <el-input v-model="pwdForm.identity_type" disabled />
        </el-form-item>
        <el-form-item label="标识">
          <el-input v-model="pwdForm.identifier" disabled />
        </el-form-item>
        <el-form-item v-if="pwdMode === 'change'" label="旧密码">
          <el-input v-model="pwdForm.old_password" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="pwdForm.new_password" type="password" show-password />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="pwdDialogVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="submitting" @click="submitPwd">
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
