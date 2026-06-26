import {ref} from 'vue'
import {resetPassword as apiResetPassword} from '@/api/user/userApi'

export const useResetPassword = () => {
    const loading = ref(false)

    async function resetPassword(data: {
        old_password: string;
        username: any;
        email: undefined | string;
        phone: undefined | string;
        new_password: any
    }) {
        loading.value = true
        try {
            return await apiResetPassword(data.username, data.old_password, data.new_password)
        } finally {
            loading.value = false
        }
    }

    return {loading, resetPassword}
}
