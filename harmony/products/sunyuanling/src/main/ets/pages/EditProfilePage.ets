import router from '@ohos.router';
import picker from '@ohos.file.picker';
import fs from '@ohos.file.fs';
import { userStore, USER_STORAGE_KEY } from '../app/UserStore';
import type { UserInfo } from '../api/user/UserTypes';
import { Request } from '../net/Request';
import { ApiRoutes } from '../constants/ApiRoutes';
import { ApiResponse } from '../net/Response';

/**
 * EditProfilePage
 * 编辑资料页：用户名 / 邮箱 / 手机 + 头像上传。
 * 对标 Android ui/screen/person/Editprofilescreen.kt。
 */
@Entry
@Component
struct EditProfilePage {
  @StorageLink(USER_STORAGE_KEY) currentUser: UserInfo | null = null;
  @State username: string = '';
  @State email: string = '';
  @State phone: string = '';
  @State saving: boolean = false;
  @State resultMsg: string = '';
  @State resultOk: boolean = false;
  @State avatarUri: string = '';

  aboutToAppear(): void {
    this.username = this.currentUser?.username ?? '';
    this.email = this.currentUser?.email ?? '';
    this.phone = this.currentUser?.phone ?? '';
  }

  async pickAvatar(): Promise<void> {
    try {
      const photoPicker = new picker.PhotoViewPicker();
      const result = await photoPicker.select({
        MIMEType: picker.PhotoViewMIMETypes.IMAGE_TYPE,
        maxSelectNumber: 1,
      });
      if (result.photoUris.length > 0) {
        this.avatarUri = result.photoUris[0];
      }
    } catch (e) {
      console.warn(`[EditProfile] pick avatar failed: ${(e as Error)?.message}`);
    }
  }

  async doSave(): Promise<void> {
    if (this.username.trim().length === 0) {
      this.resultMsg = '用户名不能为空';
      this.resultOk = false;
      return;
    }
    this.saving = true;
    this.resultMsg = '';
    try {
      const fields: Record<string, string> = {
        'username': this.username.trim(),
      };
      if (this.email.trim().length > 0) {
        fields['email'] = this.email.trim();
      }
      if (this.phone.trim().length > 0) {
        fields['phone'] = this.phone.trim();
      }

      let fileData: ArrayBuffer | undefined;
      let mimeType: string | undefined;
      let fileName: string | undefined;
      if (this.avatarUri.length > 0) {
        try {
          const file = fs.openSync(this.avatarUri, fs.OpenMode.READ_ONLY);
          const stat = fs.statSync(file.fd);
          const buf = new ArrayBuffer(stat.size);
          fs.readSync(file.fd, buf);
          fs.closeSync(file);
          fileData = buf;
          fileName = 'avatar.png';
          if (this.avatarUri.endsWith('.jpg') || this.avatarUri.endsWith('.jpeg')) {
            mimeType = 'image/jpeg';
            fileName = 'avatar.jpg';
          } else if (this.avatarUri.endsWith('.png')) {
            mimeType = 'image/png';
          } else {
            mimeType = 'image/png';
          }
        } catch (e) {
          console.warn(`[EditProfile] read avatar failed: ${(e as Error)?.message}`);
        }
      }

      const resp = await Request.postMultipart<ApiResponse<Object>>(
        ApiRoutes.USER_UPDATE_INFO,
        fields,
        fileData !== undefined ? 'avatar' : undefined,
        fileData,
        mimeType,
        fileName
      );
      this.resultMsg = resp.message ?? '保存成功';
      this.resultOk = true;
      const updated: UserInfo = {
        id: this.currentUser?.id ?? 0,
        username: this.username.trim(),
        email: this.email.trim(),
        phone: this.phone.trim(),
        avatar: this.currentUser?.avatar ?? null,
        role: this.currentUser?.role ?? null,
        status: this.currentUser?.status ?? 1,
        last_login: this.currentUser?.last_login ?? null,
        created_at: this.currentUser?.created_at ?? null,
      };
      await userStore.setCurrent(updated);
    } catch (e) {
      this.resultMsg = (e as Error)?.message ?? '保存失败';
      this.resultOk = false;
    } finally {
      this.saving = false;
    }
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('编辑资料').fontSize(18).fontWeight(FontWeight.Bold);
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      Column() {
        // 头像
        Row() {
          Text(this.currentUser?.username?.charAt(0)?.toUpperCase() ?? 'U')
            .width(64).height(64)
            .textAlign(TextAlign.Center)
            .fontSize(28)
            .fontColor('#FFFFFF')
            .backgroundColor('#007AFF')
            .borderRadius(32);
          Text('更换头像').fontSize(13).fontColor('#007AFF').margin({ left: 16 })
            .onClick(() => { this.pickAvatar(); });
        }.width('100%').margin({ bottom: 20 });

        Row() {
          Text('用户名').fontSize(14).width(80);
          TextInput({ text: this.username })
            .layoutWeight(1).height(40)
            .onChange((v: string) => { this.username = v; });
        }.width('100%').margin({ bottom: 12 });
        Row() {
          Text('邮箱').fontSize(14).width(80);
          TextInput({ text: this.email })
            .layoutWeight(1).height(40)
            .onChange((v: string) => { this.email = v; });
        }.width('100%').margin({ bottom: 12 });
        Row() {
          Text('手机号').fontSize(14).width(80);
          TextInput({ text: this.phone })
            .layoutWeight(1).height(40)
            .onChange((v: string) => { this.phone = v; });
        }.width('100%').margin({ bottom: 24 });

        Button(this.saving ? '保存中...' : '保存')
          .type(ButtonType.Capsule)
          .width('100%').height(44)
          .backgroundColor('#007AFF')
          .enabled(!this.saving)
          .onClick(() => { this.doSave(); });

        if (this.resultMsg.length > 0) {
          Text(this.resultMsg)
            .fontSize(13)
            .fontColor(this.resultOk ? '#34C759' : '#FF3B30')
            .margin({ top: 8 });
        }
      }
      .width('92%')
      .padding(16)
      .backgroundColor('#FFFFFF')
      .borderRadius(12)
      .margin({ top: 12 });
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5');
  }
}

export default EditProfilePage;
