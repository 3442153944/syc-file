import router from '@ohos.router';
import picker from '@ohos.file.picker';
import fs from '@ohos.file.fs';
import { userStore, USER_STORAGE_KEY } from '../app/UserStore';
import type { UserInfo } from '../api/user/UserTypes';
import { Request } from '../net/Request';
import { UserApi } from '../api/user/UserApi';
import { ApiRoutes } from '../constants/ApiRoutes';
import { ApiResponse } from '../net/Response';

/**
 * 头像本地大小软上限。
 * 服务端 `user.max_size`（config.yaml，当前 50MB）会硬拒，但那要等整个文件传完才报错——
 * 手机相册随手一张原图就可能几十 MB，先在本地拦一道，省得白传一趟再看到 400。
 */
const AVATAR_SOFT_LIMIT_BYTES = 10 * 1024 * 1024;

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
  /** 服务器头像加载失败 → 退回首字母占位 */
  @State avatarFailed: boolean = false;

  /** 服务器头像地址：`avatar` 已含 `static/avatar/` 目录，只拼服务器根（见 Request.staticUrl）。 */
  private serverAvatarUrl(): string {
    return Request.staticUrl(this.currentUser?.avatar ?? '');
  }

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
        // ⚠ 一律异步 fs：相册原图动辄几十 MB，同步读会把主线程钉死（appfreeze），
        //   这是本项目在鸿蒙上反复踩过的坑，见 harmony/README.md。
        const file: fs.File = await fs.open(this.avatarUri, fs.OpenMode.READ_ONLY);
        try {
          const stat: fs.Stat = await fs.stat(file.fd);
          if (stat.size > AVATAR_SOFT_LIMIT_BYTES) {
            throw new Error(`头像太大（${(stat.size / 1024 / 1024).toFixed(1)}MB），请选择 `
              + `${AVATAR_SOFT_LIMIT_BYTES / 1024 / 1024}MB 以内的图片`);
          }
          const buf: ArrayBuffer = new ArrayBuffer(stat.size);
          await fs.read(file.fd, buf);
          fileData = buf;
        } finally {
          await fs.close(file.fd);
        }
        const lower: string = this.avatarUri.toLowerCase();
        if (lower.endsWith('.jpg') || lower.endsWith('.jpeg')) {
          mimeType = 'image/jpeg';
          fileName = 'avatar.jpg';
        } else {
          // 服务端一律 decode 后重编码成 png 落盘，扩展名只用于白名单校验
          mimeType = 'image/png';
          fileName = 'avatar.png';
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
      // 更新接口只回 {code,message}，新头像的相对路径拿不到——必须回读一次 verify，
      // 否则个人中心还挂着旧头像（之前是本地拼一个 UserInfo，头像字段永远是旧的）。
      try {
        const v = await UserApi.verify();
        if (v.code === 200 && v.data !== null) {
          await userStore.setCurrent(v.data);
          this.avatarUri = '';
        }
      } catch (e) {
        // 回读失败不影响「已保存」这个事实，退回本地拼装，下次进页面再校正
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
      }
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
        // 头像：优先显示刚选的本地图，其次服务器上的，都没有才用首字母占位（对标安卓）
        Row() {
          if (this.avatarUri.length > 0) {
            Image(this.avatarUri)
              .width(64).height(64).borderRadius(32).objectFit(ImageFit.Cover);
          } else if (this.serverAvatarUrl().length > 0 && !this.avatarFailed) {
            Image(this.serverAvatarUrl())
              .width(64).height(64).borderRadius(32).objectFit(ImageFit.Cover)
              .onError(() => { this.avatarFailed = true; });
          } else {
            Text(this.currentUser?.username?.charAt(0)?.toUpperCase() ?? 'U')
              .width(64).height(64)
              .textAlign(TextAlign.Center)
              .fontSize(28)
              .fontColor('#FFFFFF')
              .backgroundColor('#007AFF')
              .borderRadius(32);
          }
          Column() {
            Text('更换头像').fontSize(14).fontColor('#007AFF')
              .onClick(() => { this.pickAvatar(); });
            Text(`支持 jpg/png，建议 ${AVATAR_SOFT_LIMIT_BYTES / 1024 / 1024}MB 以内`)
              .fontSize(11).fontColor('#9E9E9E').margin({ top: 4 });
          }.alignItems(HorizontalAlign.Start).margin({ left: 16 });
        }.width('100%').margin({ bottom: 20 }).alignItems(VerticalAlign.Center);

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
