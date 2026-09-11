<script lang="ts" setup>
import type { File as V1File } from "@/api/generated/model"
// upload/download 是 gin 面传输特例（multipart 进度 / blob 流式），不契约化，保留手写层
import { downloadFileApi, uploadFileApi } from "@@/apis/files"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import { formatDateTime } from "@@/utils/datetime"
import { fileServiceDeleteFile, fileServiceListFiles } from "@/api/generated/file-service"

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该文件？" })

const loading = ref(false)
const list = ref<V1File[]>([])
const uploadLoading = ref(false)
const uploadProgress = ref(0)
const uploadDialogVisible = ref(false)
const fileList = ref<any[]>([])
const uploadForm = reactive({
  file_directory: ""
})

async function fetchList() {
  loading.value = true
  try {
    const res = await fileServiceListFiles()
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleUpload() {
  fileList.value = []
  uploadForm.file_directory = ""
  uploadProgress.value = 0
  uploadDialogVisible.value = true
}

function handleFileChange(file: any) {
  fileList.value = [file]
}

function handleFileRemove() {
  fileList.value = []
}

async function handleSubmitUpload() {
  if (fileList.value.length === 0) {
    ElMessage.warning("请选择文件")
    return
  }
  const file = fileList.value[0].raw
  // 预校验 50MiB
  const MAX_SIZE = 50 * 1024 * 1024
  if (file.size > MAX_SIZE) {
    ElMessage.error("文件大小不能超过 50MiB")
    return
  }
  uploadLoading.value = true
  uploadProgress.value = 0
  try {
    await uploadFileApi(file, uploadForm.file_directory || undefined, (percent) => {
      uploadProgress.value = percent
    })
    ElMessage.success("上传成功")
    uploadDialogVisible.value = false
    fetchList()
  } catch (error: any) {
    ElMessage.error(error.message || "上传失败")
  } finally {
    uploadLoading.value = false
  }
}

async function handleDownload(row: V1File) {
  try {
    const blob = await downloadFileApi(row.id!)
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = row.file_name ?? ""
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch (error: any) {
    ElMessage.error(error.message || "下载失败")
  }
}

function handleDeleteAction(row: V1File) {
  handleDelete(() => fileServiceDeleteFile(row.id!), fetchList)
}

function formatSize(bytes?: number) {
  if (!bytes) return "0 B"
  const k = 1024
  const sizes = ["B", "KB", "MB", "GB"]
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / k ** i).toFixed(2)} ${sizes[i]}`
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleUpload">
        上传文件
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="file_name" label="文件名" align="center" />
        <el-table-column prop="file_directory" label="目录" align="center" />
        <el-table-column prop="extension" label="扩展名" align="center" width="80" />
        <el-table-column label="大小" align="center" width="100">
          <template #default="{ row }">
            {{ formatSize(row.size) }}
          </template>
        </el-table-column>
        <el-table-column prop="mime_type" label="MIME" align="center" />
        <el-table-column prop="content_hash" label="SHA256" align="center" width="200" show-overflow-tooltip />
        <el-table-column prop="created_by" label="上传者" align="center" />
        <el-table-column label="上传时间" align="center" width="180">
          <template #default="{ row }">
            {{ formatDateTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleDownload(row as V1File)">
              下载
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as V1File)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="uploadDialogVisible" title="上传文件" width="500px" :close-on-click-modal="false">
      <el-form label-width="100px">
        <el-form-item label="业务目录">
          <el-input v-model="uploadForm.file_directory" placeholder="可选，如 images" />
        </el-form-item>
        <el-form-item label="选择文件">
          <el-upload
            :file-list="fileList"
            :on-change="handleFileChange"
            :on-remove="handleFileRemove"
            :auto-upload="false"
            :limit="1"
            drag
          >
            <el-icon class="el-icon--upload">
              <upload-filled />
            </el-icon>
            <div class="el-upload__text">
              拖拽文件到此处或 <em>点击上传</em>
            </div>
            <template #tip>
              <div class="el-upload__tip">
                文件大小不超过 50MiB
              </div>
            </template>
          </el-upload>
        </el-form-item>
        <el-form-item v-if="uploadLoading" label="进度">
          <el-progress :percentage="uploadProgress" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="uploadDialogVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="uploadLoading" @click="handleSubmitUpload">
          上传
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
