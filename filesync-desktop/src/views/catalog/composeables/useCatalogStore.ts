import { defineStore } from 'pinia'
import { ref } from 'vue'
import { traverseDirectory } from '@/api/file/fileApi'
import type { FileItem } from '@/api/file/fileTypes'

export const useCatalogStore = defineStore('catalog', () => {
    const currentPath = ref<string>('')
    const parentPath = ref<string>('')
    const items = ref<FileItem[]>([])
    const loading = ref<boolean>(false)
    const error = ref('')
    let requestId = 0

    const fetchDirectory = async (targetPath: string) => {
        const id = ++requestId
        loading.value = true
        error.value = ''
        try {
            const res = await traverseDirectory(targetPath)
            // 快速切换目录时，迟到的旧请求不能覆盖当前目录。
            if (id !== requestId) return
            currentPath.value = res.current_path
            parentPath.value = res.parent_path
            items.value = res.items || []
        } catch (cause) {
            if (id === requestId) error.value = `${targetPath}：${String(cause)}`
        } finally {
            if (id === requestId) loading.value = false
        }
    }

    const goParent = async () => {
        if (parentPath.value) {
            await fetchDirectory(parentPath.value)
        }
    }

    const parseMode = (modeStr: string) => {
        if (!modeStr || modeStr.length < 4) return []
        const tags = []
        const ownerPerms = modeStr.substring(1, 4)
        if (ownerPerms.includes('r')) tags.push({ label: '可读', type: 'info' })
        if (ownerPerms.includes('w')) tags.push({ label: '可写', type: 'success' })
        if (ownerPerms.includes('x')) {
            const isDir = modeStr.charAt(0) === 'd'
            tags.push({ label: isDir ? '可进入' : '可执行', type: 'warning' })
        }
        if (tags.length === 0) tags.push({ label: '无权限', type: 'error' })
        return tags
    }

    return { currentPath, parentPath, items, loading, error, fetchDirectory, goParent, parseMode }
})
