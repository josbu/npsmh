# 特定平台

这一页收集 Android、OpenWrt、群晖等特定平台入口。

如果你只是普通 Linux、Windows 或 macOS 部署，优先看 [Docker 安装](/getting-started/install-docker)、[脚本安装](/getting-started/install-script) 或 [发布包安装](/getting-started/install-release)。

## Android

- APK 项目：[djylb/npsclient](https://github.com/djylb/npsclient)
- Google Play：[NPS Client](https://play.google.com/store/apps/details?id=com.duanlab.npsclient)
- APK 下载
  - [Universal](https://github.com/djylb/npsclient/releases/latest/download/app-universal-release.apk)
  - [ARM64](https://github.com/djylb/npsclient/releases/latest/download/app-arm64-v8a-release.apk)
  - [ARM32](https://github.com/djylb/npsclient/releases/latest/download/app-armeabi-v7a-release.apk)
  - [x86_64](https://github.com/djylb/npsclient/releases/latest/download/app-x86_64-release.apk)

Termux：

- [Server arm64](https://github.com/djylb/nps/releases/latest/download/android_arm64_server.tar.gz)
- [Client arm64](https://github.com/djylb/nps/releases/latest/download/android_arm64_client.tar.gz)

## OpenWrt

- [djylb/nps-openwrt](https://github.com/djylb/nps-openwrt)

## 群晖

群晖更推荐使用 Docker 方式运行。历史 `.spk` 方案已不再作为主维护方式。

- 客户端容器启动参数可参考 [NPC 命令行参数](/reference/npc-cli)
- 社区中也有第三方套件信息，可在 [交流](/community/discuss) 中查看
