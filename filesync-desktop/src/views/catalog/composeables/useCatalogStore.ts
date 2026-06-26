import { defineStore } from 'pinia'
import { ref } from 'vue'
import { traverseDirectory } from '@/api/file/fileApi'
import type { FileItem } from '@/api/file/fileTypes'

export const useCatalogStore = defineStore('catalog', () => {
    const currentPath = ref<string>('')
    const parentPath = ref<string>('')
    const items = ref<FileItem[]>([])
    const loading = ref<boolean>(false)

    const fetchDirectory = async (targetPath: string) => {
        loading.value = true
        try {
            const res = await traverseDirectory(targetPath)
            currentPath.value = res.current_path
            parentPath.value = res.parent_path
            items.value = res.items || []
        } catch (error) {
            console.error('读取目录失败', error)
        } finally {
            loading.value = false
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

    return { currentPath, parentPath, items, loading, fetchDirectory, goParent, parseMode }
})
