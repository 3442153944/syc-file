import {ref} from 'vue'
import {register as apiRegister} from '@/api/user/userApi'

export const useRegister = () => {
    const loading = ref(false)

    async function register(data: { username: any; password: any; email: any; phone: any }) {
        loading.value = true
        try {
            return await apiRegister(data.username, data.password, data.email)
        } finally {
            loading.value = false
        }
    }

    return {loading, register}
}
