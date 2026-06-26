import { login as apiLogin, verify as apiVerify } from '@/api/user/userApi'

export const useLogin = () => {
    async function login(data: { username: string; password: string }) {
        return await apiLogin(data.username, data.password)
    }

    async function verify() {
        return await apiVerify()
    }

    return { login, verify }
}
