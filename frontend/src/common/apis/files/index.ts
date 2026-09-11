// 文件域传输特例层：upload/download 走 gin 面的 multipart/blob 传输形态，
// 不在 proto 契约内（gateway 面 JSON base64 / JSON bytes 是另一形态），
// 故不参与 orval 生成，保留手写。列表/删除/详情已由生成的
// file-service.ts 承接（见 src/api/generated/file-service.ts）。
import type { FileItem } from "@@/types/api"
import { request } from "@/http/axios"

/** 上传文件（multipart/form-data，gin 面） */
export function uploadFileApi(file: File, fileDirectory?: string, onProgress?: (percent: number) => void) {
  const formData = new FormData()
  formData.append("file", file)
  if (fileDirectory) formData.append("directory", fileDirectory)
  return request<{ file: FileItem }>({
    url: "v1/file/upload",
    method: "post",
    data: formData,
    headers: { "Content-Type": "multipart/form-data" },
    timeout: 120000,
    onUploadProgress: onProgress
      ? (e) => {
          if (e.total) onProgress(Math.round((e.loaded * 100) / e.total))
        }
      : undefined
  })
}

/** 下载文件（blob 流式，gin 面） */
export function downloadFileApi(id: string) {
  return request<Blob>({
    url: `v1/file/${id}/download`,
    method: "get",
    responseType: "blob"
  })
}
