<script lang="ts" setup>
import type { DictEntry, DictType } from "@/api/generated/model"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import { formatDateTime } from "@@/utils/datetime"
import {
  dictEntryServiceCreateDictEntry,
  dictEntryServiceDeleteDictEntry,
  dictEntryServiceListDictEntries,
  dictEntryServiceUpdateDictEntry
} from "@/api/generated/dict-entry-service"
import {
  dictTypeServiceCreateDictType,
  dictTypeServiceDeleteDictType,
  dictTypeServiceListDictTypes,
  dictTypeServiceUpdateDictType
} from "@/api/generated/dict-type-service"

const { handleDelete } = useConfirmAction()

const activeTab = ref("types")
const typeLoading = ref(false)
const entryLoading = ref(false)
const typeList = ref<DictType[]>([])
const entryList = ref<DictEntry[]>([])
const selectedTypeCode = ref("")

// 类型弹窗
const typeDialogVisible = ref(false)
const typeDialogTitle = ref("")
const typeSubmitting = ref(false)
const typeFormRef = useTemplateRef("typeFormRef")
const typeForm = reactive<DictType>({ id: "", type_name: "", sort_order: 0, enabled: true, remark: "", created_at: "", updated_at: "" })
const typeRules = {
  id: [{ required: true, message: "请输入类型编码", trigger: "blur" }],
  type_name: [{ required: true, message: "请输入类型名称", trigger: "blur" }]
}

// 条目弹窗
const entryDialogVisible = ref(false)
const entryDialogTitle = ref("")
const entrySubmitting = ref(false)
const entryFormRef = useTemplateRef("entryFormRef")
const entryForm = reactive<DictEntry>({ id: "", type_code: "", value: "", label: "", sort_order: 0, enabled: true, remark: "", created_at: "", updated_at: "" })
const entryRules = {
  type_code: [{ required: true, message: "请输入类型编码", trigger: "blur" }],
  value: [{ required: true, message: "请输入条目值", trigger: "blur" }],
  label: [{ required: true, message: "请输入标签", trigger: "blur" }]
}

async function fetchTypes() {
  typeLoading.value = true
  try {
    const res = await dictTypeServiceListDictTypes()
    typeList.value = res.items || []
  } finally {
    typeLoading.value = false
  }
}

async function fetchEntries(typeCode?: string) {
  entryLoading.value = true
  try {
    const res = await dictEntryServiceListDictEntries(typeCode ? { type_code: typeCode } : undefined)
    entryList.value = res.items || []
  } finally {
    entryLoading.value = false
  }
}

function handleTabChange(name: string | number) {
  if (name === "entries" && entryList.value.length === 0) {
    fetchEntries()
  }
}

// 类型操作
function handleCreateType() {
  Object.assign(typeForm, { id: "", type_name: "", sort_order: 0, enabled: true, remark: "", created_at: "", updated_at: "" })
  typeDialogTitle.value = "新增字典类型"
  typeDialogVisible.value = true
}

function handleEditType(row: DictType) {
  Object.assign(typeForm, row)
  typeDialogTitle.value = "编辑字典类型"
  typeDialogVisible.value = true
}

function handleSubmitType() {
  typeFormRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    typeSubmitting.value = true
    try {
      if (typeForm.created_at) {
        await dictTypeServiceUpdateDictType(typeForm.id!, {
          type_name: typeForm.type_name,
          sort_order: typeForm.sort_order,
          sort_order_set: true,
          enabled: typeForm.enabled,
          enabled_set: true,
          remark: typeForm.remark
        })
      } else {
        await dictTypeServiceCreateDictType({
          id: typeForm.id,
          type_name: typeForm.type_name,
          sort_order: typeForm.sort_order,
          remark: typeForm.remark
        })
      }
      ElMessage.success("保存成功")
      typeDialogVisible.value = false
      fetchTypes()
    } finally {
      typeSubmitting.value = false
    }
  })
}

function handleDeleteType(row: DictType) {
  handleDelete(() => dictTypeServiceDeleteDictType(row.id!), fetchTypes)
}

// 条目操作
function handleCreateEntry() {
  Object.assign(entryForm, { id: "", type_code: selectedTypeCode.value || "", value: "", label: "", sort_order: 0, enabled: true, remark: "", created_at: "", updated_at: "" })
  entryDialogTitle.value = "新增字典条目"
  entryDialogVisible.value = true
}

function handleEditEntry(row: DictEntry) {
  Object.assign(entryForm, row)
  entryDialogTitle.value = "编辑字典条目"
  entryDialogVisible.value = true
}

function handleSubmitEntry() {
  entryFormRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    entrySubmitting.value = true
    try {
      if (entryForm.created_at) {
        await dictEntryServiceUpdateDictEntry(entryForm.id!, {
          label: entryForm.label,
          numeric: entryForm.numeric,
          numeric_set: entryForm.numeric !== undefined,
          sort_order: entryForm.sort_order,
          sort_order_set: true,
          enabled: entryForm.enabled,
          enabled_set: true,
          remark: entryForm.remark
        })
      } else {
        await dictEntryServiceCreateDictEntry({
          type_code: entryForm.type_code,
          value: entryForm.value,
          label: entryForm.label,
          numeric: entryForm.numeric,
          sort_order: entryForm.sort_order,
          remark: entryForm.remark
        })
      }
      ElMessage.success("保存成功")
      entryDialogVisible.value = false
      fetchEntries(selectedTypeCode.value || undefined)
    } finally {
      entrySubmitting.value = false
    }
  })
}

function handleDeleteEntry(row: DictEntry) {
  handleDelete(() => dictEntryServiceDeleteDictEntry(row.id!), () => fetchEntries(selectedTypeCode.value || undefined))
}

function handleFilterByType() {
  fetchEntries(selectedTypeCode.value || undefined)
}

onMounted(fetchTypes)
</script>

<template>
  <div class="app-container">
    <el-tabs v-model="activeTab" @tab-change="handleTabChange">
      <el-tab-pane label="字典类型" name="types">
        <el-card shadow="never" class="search-wrapper">
          <el-button type="primary" @click="handleCreateType">
            新增字典类型
          </el-button>
        </el-card>
        <el-card shadow="never">
          <el-table v-loading="typeLoading" :data="typeList" border stripe>
            <el-table-column prop="id" label="类型编码" align="center" />
            <el-table-column prop="type_name" label="类型名称" align="center" />
            <el-table-column prop="sort_order" label="排序" align="center" width="80" />
            <el-table-column label="状态" align="center" width="80">
              <template #default="{ row }">
                <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
                  {{ row.enabled ? "启用" : "禁用" }}
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
                <el-button type="primary" link @click="handleEditType(row as DictType)">
                  编辑
                </el-button>
                <el-button type="danger" link @click="handleDeleteType(row as DictType)">
                  删除
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="字典条目" name="entries">
        <el-card shadow="never" class="search-wrapper">
          <el-form :inline="true">
            <el-form-item label="类型编码">
              <el-input v-model="selectedTypeCode" placeholder="如 gender" clearable @clear="handleFilterByType" @keyup.enter="handleFilterByType" />
            </el-form-item>
            <el-form-item>
              <el-button type="primary" @click="handleFilterByType">
                查询
              </el-button>
              <el-button type="primary" @click="handleCreateEntry">
                新增条目
              </el-button>
            </el-form-item>
          </el-form>
        </el-card>
        <el-card shadow="never">
          <el-table v-loading="entryLoading" :data="entryList" border stripe>
            <el-table-column prop="type_code" label="类型编码" align="center" />
            <el-table-column prop="value" label="条目值" align="center" />
            <el-table-column prop="label" label="标签" align="center" />
            <el-table-column prop="sort_order" label="排序" align="center" width="80" />
            <el-table-column label="状态" align="center" width="80">
              <template #default="{ row }">
                <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
                  {{ row.enabled ? "启用" : "禁用" }}
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
                <el-button type="primary" link @click="handleEditEntry(row as DictEntry)">
                  编辑
                </el-button>
                <el-button type="danger" link @click="handleDeleteEntry(row as DictEntry)">
                  删除
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <!-- 类型弹窗 -->
    <el-dialog v-model="typeDialogVisible" :title="typeDialogTitle" width="500px" :close-on-click-modal="false">
      <el-form ref="typeFormRef" :model="typeForm" :rules="typeRules" label-width="100px">
        <el-form-item label="类型编码" prop="id">
          <el-input v-model="typeForm.id" placeholder="如 gender" :disabled="!!typeForm.created_at" />
        </el-form-item>
        <el-form-item label="类型名称" prop="type_name">
          <el-input v-model="typeForm.type_name" placeholder="如 性别" />
        </el-form-item>
        <el-form-item label="排序" prop="sort_order">
          <el-input-number v-model="typeForm.sort_order" :min="0" />
        </el-form-item>
        <el-form-item label="启用" prop="enabled">
          <el-switch v-model="typeForm.enabled" />
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="typeForm.remark" type="textarea" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="typeDialogVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="typeSubmitting" @click="handleSubmitType">
          确定
        </el-button>
      </template>
    </el-dialog>

    <!-- 条目弹窗 -->
    <el-dialog v-model="entryDialogVisible" :title="entryDialogTitle" width="500px" :close-on-click-modal="false">
      <el-form ref="entryFormRef" :model="entryForm" :rules="entryRules" label-width="100px">
        <el-form-item label="类型编码" prop="type_code">
          <el-input v-model="entryForm.type_code" placeholder="如 gender" :disabled="!!entryForm.created_at" />
        </el-form-item>
        <el-form-item label="条目值" prop="value">
          <el-input v-model="entryForm.value" placeholder="如 male" :disabled="!!entryForm.created_at" />
        </el-form-item>
        <el-form-item label="标签" prop="label">
          <el-input v-model="entryForm.label" placeholder="如 男" />
        </el-form-item>
        <el-form-item label="排序" prop="sort_order">
          <el-input-number v-model="entryForm.sort_order" :min="0" />
        </el-form-item>
        <el-form-item label="启用" prop="enabled">
          <el-switch v-model="entryForm.enabled" />
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="entryForm.remark" type="textarea" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="entryDialogVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="entrySubmitting" @click="handleSubmitEntry">
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
