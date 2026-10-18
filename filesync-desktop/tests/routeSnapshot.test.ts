import { test } from 'node:test'
import assert from 'node:assert/strict'
import { shouldApplyRouteSnapshot } from '../src/utils/routeSnapshot.ts'

test('菜单版本未变但空路径恢复正常时，仍重新注册路由', () => {
  const current = { version: 'same', level: 2, routes: [{ code: 'admin.users', path: '' }] }
  const fixed = { version: 'same', level: 2, routes: [{ code: 'admin.users', path: '/admin/users' }] }
  assert.equal(shouldApplyRouteSnapshot(current, fixed), true)
})

test('权限级别变化或从缓存恢复时，重新应用相同路由', () => {
  const current = { version: 'same', level: 2, routes: [{ path: '/file/list' }] }
  assert.equal(shouldApplyRouteSnapshot(current, { ...current, level: 1 }), true)
  assert.equal(shouldApplyRouteSnapshot(current, current, true), true)
})

test('版本、级别和内容均一致时不重复注册', () => {
  const current = { version: 'same', level: 2, routes: [{ path: '/file/list' }] }
  assert.equal(shouldApplyRouteSnapshot(current, structuredClone(current)), false)
  assert.equal(shouldApplyRouteSnapshot(current, { ...current, version: 'new' }), true)
})
