import {test} from 'node:test'
import assert from 'node:assert/strict'
import {desktopVersionCode, inferDesktopArtifact} from '../src/utils/releaseTarget.ts'
test('stable desktop versions have an unambiguous integer code', () => {
 assert.equal(desktopVersionCode('0.1.1'),101)
 assert.equal(desktopVersionCode('12.34.56'),123456)
 for (const v of ['1.100.0','01.2.3','1.2.3-beta','1.2','9999999999999999.1.1']) assert.equal(desktopVersionCode(v),null)
})
test('Tauri installer names recover bundle architecture and version', () => {
 assert.deepEqual(inferDesktopArtifact('filesync-desktop_0.1.1_x64-setup.exe'), {version:'0.1.1',arch:'x86_64',bundle:'nsis'})
 assert.deepEqual(inferDesktopArtifact('filesync-desktop_0.1.1_arm64.deb'), {version:'0.1.1',arch:'aarch64',bundle:'deb'})
 assert.deepEqual(inferDesktopArtifact('filesync-desktop_0.1.1_amd64.AppImage'), {version:'0.1.1',arch:'x86_64',bundle:'appimage'})
})
