<script lang="ts" setup>
import type { Language } from "@/api/generated/model"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import {
  languageServiceCreateLanguage,
  languageServiceDeleteLanguage,
  languageServiceListLanguages,
  languageServiceUpdateLanguage
} from "@/api/generated/language-service"

defineOptions({ name: "Languages" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该语言？" })

const loading = ref(false)
const list = ref<Language[]>([])
const dialogVisible = ref(false)
const dialogTitle = ref("")
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

function defaultForm(): Language {
  return {
    id: "",
    language_name: "",
    native_name: "",
    is_default: false,
    is_enabled: true,
    sort_order: 0
  }
}
const form = reactive<Language>(defaultForm())
const isEdit = ref(false)

const rules = {
  id: [{ required: true, message: "请输入语言代码（如 zh-CN）", trigger: "blur" }],
  language_name: [{ required: true, message: "请输入语言名称", trigger: "blur" }]
}

async function fetchList() {
  loading.value = true
  try {
    const res = await languageServiceListLanguages()
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, defaultForm())
  isEdit.value = false
  dialogTitle.value = "新增语言"
  dialogVisible.value = true
}

function handleEdit(row: Language) {
  Object.assign(form, { ...row })
  isEdit.value = true
  dialogTitle.value = "编辑语言"
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      if (isEdit.value) {
        await languageServiceUpdateLanguage(form.id!, {
          language_name: form.language_name,
          native_name: form.native_name,
          is_default: form.is_default,
          is_default_set: true,
          is_enabled: form.is_enabled,
          is_enabled_set: true,
          sort_order: form.sort_order
        })
      } else {
        await languageServiceCreateLanguage({
          id: form.id,
          language_name: form.language_name,
          native_name: form.native_name,
          is_default: form.is_default,
          is_enabled: form.is_enabled,
          sort_order: form.sort_order
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

function handleDeleteAction(row: Language) {
  handleDelete(() => languageServiceDeleteLanguage(row.id!), fetchList)
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleCreate">
        新增语言
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="id" label="语言代码" align="center" width="120" />
        <el-table-column prop="language_name" label="语言名称" />
        <el-table-column prop="native_name" label="本地名称" />
        <el-table-column label="默认" align="center" width="80">
          <template #default="{ row }">
            <el-tag v-if="row.is_default" type="success" size="small">
              是
            </el-tag>
            <span v-else>否</span>
          </template>
        </el-table-column>
        <el-table-column label="启用" align="center" width="80">
          <template #default="{ row }">
            <el-tag :type="row.is_enabled ? 'success' : 'info'" size="small">
              {{ row.is_enabled ? "启用" : "禁用" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="sort_order" label="排序" align="center" width="70" />
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row as Language)">
              编辑
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as Language)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="520px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
        <el-form-item label="语言代码" prop="id">
          <el-input v-model="form.id" placeholder="如 zh-CN" :disabled="isEdit" />
        </el-form-item>
        <el-form-item label="语言名称" prop="language_name">
          <el-input v-model="form.language_name" />
        </el-form-item>
        <el-form-item label="本地名称" prop="native_name">
          <el-input v-model="form.native_name" />
        </el-form-item>
        <el-form-item label="默认语言" prop="is_default">
          <el-switch v-model="form.is_default" />
        </el-form-item>
        <el-form-item label="启用" prop="is_enabled">
          <el-switch v-model="form.is_enabled" />
        </el-form-item>
        <el-form-item label="排序" prop="sort_order">
          <el-input-number v-model="form.sort_order" :min="0" />
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
