# lacron

基于 Alpine 的轻量定时任务容器：Go 内置秒级 cron 调度器、管理 API 和 WebUI，支持 Node.js LTS 与 sh 脚本，可自行安装 Alpine 软件包。

## 功能

- 简洁扁平化中文界面，支持浅色、深色和跟随系统，适配手机。
- 创建、编辑、删除、启用和暂停 cron 任务，支持常用时间预设、搜索和下次执行时间。
- 六字段秒级调度，兼容原有五字段分钟级任务，界面时间显示到秒。
- 执行 Shell / JavaScript 命令；暂停的任务也可手动运行。
- 显示运行状态、触发来源、耗时、退出码和输出日志，每 5 秒刷新。
- 同一任务防重入，可配置总并发数；超时和容器关闭会终止任务进程组。
- 原子写入 JSON，重启恢复任务和执行记录；无需数据库或外部前端服务。
- 访问令牌鉴权，拒绝跨站写请求；脚本默认使用 UID/GID `10001:10001`，不继承管理令牌等服务环境变量。
- MIT 开源协议。

## 快速启动

需要 Docker Engine 和 Docker Compose 插件。

```sh
git clone https://github.com/dismay712/lacron.git
cd lacron
docker compose build --pull
docker compose up -d
# 未设置 LACRON_TOKEN 时，首次启动自动生成随机令牌；重启后保留。
docker compose exec lacron cat /data/admin.token
```

打开 <http://localhost:8080>，输入令牌，点击「新建任务」。可以直接使用内置示例：

```text
名称：Node.js 示例
Cron：*/5 * * * * *
命令：node /scripts/hello.js
工作目录：/scripts
超时：300 秒
```

这个例子每 5 秒执行一次。Shell 示例命令：`sh /scripts/hello.sh`，也可以直接填写多行 sh 命令。将自己的脚本放到仓库的 `scripts/` 目录即可，Compose 将它只读挂载到容器 `/scripts`；这个挂载不影响根文件系统安装软件包。脚本文件及父目录必须允许 UID 10001 读取、遍历。需要第三方 npm 包时，可在自定义镜像构建期间安装到脚本项目中。

Compose 默认时区为 `Asia/Shanghai`，仅绑定宿主机 `127.0.0.1:8080`。远程访问可使用 SSH 端口转发或配置 HTTPS 反向代理。代理应保留 `Host` 和 `Authorization` 请求头。管理令牌有执行容器内命令的权限，请只交给可信管理员。

可复制 `.env.example` 为 `.env` 调整端口、时区、并发数，或设置自己的随机令牌（至少 24 字节、无空白）。令牌只在浏览器当前标签页会话内保存，退出登录即清除。自定义令牌应使用随机生成器生成，不要提交到 Git。

## 镜像与版本更新

多阶段 Dockerfile 默认使用：

| 阶段 | 镜像 | 用途 |
| --- | --- | --- |
| 运行环境 | `alpine:latest` | 最新稳定 Alpine；包含 BusyBox `crond` |
| JavaScript | `node:lts-alpine` | 官方最新 LTS 及其最新补丁，包含 `node`、`npm`、`npx` |
| Go 编译 | `golang:alpine` | 编译静态 Go 二进制，前端资源直接嵌入 |

构建时检查 `process.release.lts` 非空，防止误用 Current 版。Node 运行时复制自官方 musl 镜像，最终镜像从 `alpine:latest` 构建并安装 `libstdc++`、CA 证书、时区数据及 `tini`。

`latest` 和 `lts-alpine` 在**构建时**解析。已运行的容器不会自动升级：

```sh
docker compose build --pull --no-cache
docker compose up -d
docker compose exec lacron cat /etc/alpine-release
docker compose exec lacron node -p 'process.version + " LTS=" + process.release.lts'
```

CI 每次推送、PR 和每周一都会重新测试并拉取基础镜像。可在仓库 [Actions](https://github.com/dismay712/lacron/actions) 的成功运行中下载 Linux amd64 二进制和 `docker save` 镜像压缩包，保留 14 天。下载解压后的镜像文件可这样导入：

```sh
gzip -dc lacron-image-amd64.tar.gz | docker load
```

## GitHub Actions 自动构建与发布

工作流位于 [`.github/workflows/ci.yml`](.github/workflows/ci.yml)，支持 `main` 推送、Pull Request、Actions 页面手动运行，以及每周一 UTC 03:17（北京时间 11:17）计划运行。

`verify` 作业拉取最新基础镜像，执行 Go race/vet、真实 cron / Node.js 容器测试和 Chromium 界面测试，随后导出构建产物。`publish` 作业只在 `main` 的验证成功后执行，复用同一份已测试镜像并发布到 GHCR：

- `ghcr.io/dismay712/lacron:latest`：最近一次发布。
- `ghcr.io/dismay712/lacron:sha-<12位提交SHA>`：对应提交的构建。
- 当前发布平台为 `linux/amd64`；镜像带有源码仓库、MIT 协议和完整提交 SHA 标签。

发布使用 GitHub 自带的 `GITHUB_TOKEN`，仅发布作业申请 `packages: write` 权限，无需额外配置 Docker Hub 账号或 PAT。PR 只验证，不发布。发布后会重新拉取两个标签、核对镜像 ID 和架构，并将镜像地址与 digest 写入 Actions Summary。

GHCR 首次发布的包默认是私有的。若需匿名拉取，请由仓库所有者在 [包设置](https://github.com/users/dismay712/packages/container/lacron/settings) 将可见性改为 Public；工作流会检查匿名访问是否可用。私有包可使用带 `read:packages` 权限的 GitHub PAT（classic）登录：

```sh
podman login ghcr.io -u dismay712
```

拉取和启动镜像（默认权限、可写根文件系统）：

```sh
podman pull ghcr.io/dismay712/lacron:latest
podman run -d --name lacron \
  --user 0:0 \
  --restart=unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e TZ=Asia/Shanghai \
  -v lacron-data:/data \
  ghcr.io/dismay712/lacron:latest
podman exec lacron cat /data/admin.token
```

挂载自己的脚本时，可在镜像名前添加 `-v "$PWD/scripts:/scripts:ro,Z"`，并确保脚本允许容器 UID 10001 读取。

## 安装 Alpine 软件包

默认 Compose 与上述 Podman 命令使用**可写根文件系统和容器默认能力集**，管理员可以直接安装：

```sh
podman exec --user 0:0 lacron apk add --no-cache bash curl jq
# Docker Compose 对应命令：
docker compose exec --user 0:0 lacron apk add --no-cache bash curl jq
```

安装完成后，任务可使用 `sh /scripts/task.sh`、`bash /scripts/task.bash`、`curl`、`jq` 等命令。`/bin/sh` 默认为 Alpine BusyBox ash；需要 Bash 语法的脚本请安装 bash 并使用 `bash` 执行。任务仍以 UID/GID `10001:10001` 运行；装包由管理员执行。

临时安装的软件包在同一容器重启后保留，删除并重建容器时会丢失。若希望重建后自动安装，将以下内容放入 `.env`，然后执行 `docker compose up -d --force-recreate`：

```dotenv
LACRON_APK_PACKAGES=bash curl jq
```

Podman 则在 `run` 时添加 `-e 'LACRON_APK_PACKAGES=bash curl jq'`。每次启动都会在 Go 服务运行前调用 `apk add --no-cache`，已安装的包由 apk 检查；配置非空时需要网络访问软件源、容器内 root 与可写根文件系统，失败会停止启动。包名按空白拆分，不执行其中的 shell 语法。

也可在构建时安装，软件包随镜像保留，容器启动时不必访问软件源：

```sh
podman build --pull --build-arg 'EXTRA_APK_PACKAGES=bash curl jq' -t lacron:custom .
```

如果之前使用了 `--read-only`，需停止并重建容器，去掉该参数及 `--cap-drop=ALL` / 手工 `--cap-add` 配置，保留 `-v lacron-data:/data`。任务、令牌与日志继续保存在原卷内；无需删除数据卷。

## 自定义镜像构建

需要固定基础镜像时可覆盖 build args（可使用完整 tag 或 digest）：

```sh
docker build --pull \
  --build-arg ALPINE_IMAGE=alpine:latest \
  --build-arg NODE_IMAGE=node:lts-alpine \
  --build-arg GO_IMAGE=golang:alpine \
  -t lacron:local .
```

Dockerfile 支持 Buildx 的 `linux/amd64`、`linux/arm64` 目标；CI 实际运行验证 amd64。其他架构须确认相应官方基础镜像可用。

## 调度规则

支持六字段数字 cron：`秒 分 时 日 月 星期`；原有五字段 `分 时 日 月 星期` 自动按第 0 秒执行，无需修改已有任务。

| 字段 | 范围 |
| --- | --- |
| 秒 | 0–59；五字段省略时固定为 0 |
| 分 | 0–59 |
| 时 | 0–23 |
| 日 | 1–31 |
| 月 | 1–12 |
| 星期 | 0–6，0 = 周日 |

支持 `*`、逗号列表、递增范围和步长（例如 `*/5`、`1-5`、`1,15`、`1-20/2`）。不支持月份/星期英文名、`7` 代表周日或 `@reboot` 等宏。

| 表达式 | 说明 |
| --- | --- |
| `* * * * * *` | 每秒 |
| `*/5 * * * * *` | 每分钟第 0、5、10…55 秒 |
| `15,45 * * * * *` | 每分钟第 15、45 秒 |
| `0 * * * * *` | 每分钟第 0 秒 |
| `* * * * *` | 兼容旧格式，每分钟第 0 秒 |
| `0 0 9 * * 1-5` | 工作日 09:00:00 |

「日」和「星期」均受限时按 **OR** 匹配；选择整个范围等价于 `*`，日期步长 `*/2` 从 1 日开始。Go 使用同一解析器计算预览和触发，不再将管理任务交给只能精确到分钟的 BusyBox crond。镜像仍包含 `crond` 命令，但不另起一个 crond 调度这些任务。不存在的日期（例如 2 月 31 日）不执行，界面会提示未来 8 年内无匹配时间。

时区由 `TZ` 决定，界面也按该时区显示时间（含秒）。调度循环对齐整秒，创建、修改、暂停在保存成功后即被下一轮采用。同一进程内每个任务每个时刻最多触发一次；容器停止或调度延迟期间的错过时刻不补跑。夏令时跳过的本地时间不执行，回拨时重复出现的本地时间可再次匹配；系统时钟回拨不会重放该任务已经触发过的绝对时刻。这是秒级任务调度，不保证硬实时或毫秒级准确性。

同一任务运行期间会跳过重复触发；达到全局并发上限时也会跳过，不排队。编辑运行中的任务只影响后续执行；任务正在运行时无法删除。手动执行可用于暂停的任务。

## 持久化与运行权限

- `/data/jobs.json`：任务配置。
- `/data/crontabs/root`：仅旧版本遗留；升级启动时清空旧管理条目，避免重复调度，新安装无需该文件。
- `/data/runs.json`、`/data/logs/`：全局最近 200 次执行及日志，每次输出上限 256 KiB；超过上限继续消费输出并丢弃，防止日志填满磁盘。被淘汰记录的日志一并删除。
- `/data/admin.token`：自动生成的管理令牌，只有 root 可读。
- `/run/lacron/runner.sock`：兼容本地 `lacron trigger` 的 Unix socket，不对外暴露；秒级调度直接调用执行器。

通过命名卷 `lacron-data` 持久化，`docker compose down` 不删除数据；`down -v` 会删除数据。备份前停止容器，完整备份数据卷；恢复时保留文件权限。通过 WebUI/API 管理任务。单个数据目录只允许一个服务实例，使用文件锁防止多实例并发写入。

主服务默认以容器内 root 运行，以便启动时装包并在执行任务时切换到普通 UID/GID；使用 `tini` 回收孤儿进程。Compose 默认保留容器默认能力集，支持管理员装包；未使用 privileged。非 root 本地运行时，脚本使用当前用户。

需要只读部署时，可自行启用 `--read-only`，为 `/run` 和 `/tmp` 挂载可写 tmpfs，再用 `--cap-drop=ALL --cap-add=SETUID --cap-add=SETGID --cap-add=KILL`；这种模式请在构建阶段安装包，并保持 `LACRON_APK_PACKAGES` 为空。CI 同时验证默认可写模式和可选只读模式。

工作目录默认为 `/scripts`。根文件系统可写仍遵循 Linux 文件权限，普通任务不能任意修改 root 所有的文件。需要保存脚本输出时，应额外挂载可写目录（例如 `/work`）并赋予运行 UID/GID 写权限；`/tmp` 可作临时目录，重建容器后不保留。服务重启时，上次未完成的执行会标记为「已中断」。不要给任务挂载 Docker socket 或主机敏感目录。脚本具有容器网络访问能力，这不是面向不可信租户的代码沙箱。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TZ` | 镜像 `UTC`；Compose `Asia/Shanghai` | 全局 IANA 时区 |
| `LACRON_ADDR` | `:8080` | HTTP 监听地址 |
| `LACRON_DATA_DIR` | `/data` | 持久化目录，绝对路径 |
| `LACRON_SOCKET` | `/run/lacron/runner.sock` | 内部 Unix socket，绝对路径 |
| `LACRON_TOKEN` | 自动生成 | 至少 24 字节的访问令牌 |
| `LACRON_TOKEN_FILE` | 空 | 从 Docker secret 等文件读取令牌；与 `LACRON_TOKEN` 互斥 |
| `LACRON_MAX_CONCURRENT` | `4` | 全局并发数，1–32 |
| `LACRON_APK_PACKAGES` | 空 | 启动时安装的 Alpine 软件包，以空白分隔；需 root 和可写根文件系统 |
| `LACRON_RUN_UID` / `LACRON_RUN_GID` | `10001` / `10001` | 用户命令的非零 UID/GID |
| `LACRON_SCRIPT_PATH` | `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` | 用户命令的 PATH |
| `LACRON_CRON` | `true` | 是否启用 Go 秒级调度器；关闭后仍可手动执行 |

如果在 Compose 中使用 `LACRON_TOKEN_FILE`，请在自己的 Compose override 中添加只读 secret 挂载及该环境变量；保留 `LACRON_TOKEN` 为空。任务的默认超时为 300 秒，可逐个设为 1–86400 秒。

## API

所有 `/api/*` 请求需要 `Authorization: Bearer <token>`，`GET /healthz` 无需鉴权。`/healthz` 同时检查秒级调度器状态；`/api/system` 返回 `scheduler: "go"`、`schedule_precision: "second"` 和兼容的 `cron_enabled` / `cron_running` 字段。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/system` | 版本、时区和 cron 状态 |
| GET / POST | `/api/jobs` | 查询 / 新建任务 |
| PUT / DELETE | `/api/jobs/{id}` | 更新 / 删除任务 |
| POST | `/api/jobs/{id}/run` | 手动执行，返回 202 和执行 ID |
| GET | `/api/runs?job_id={id}` | 最近执行，可选按任务过滤 |
| GET | `/api/runs/{id}/log` | 输出日志：`{"output":"..."}` |

新建 / 更新任务 JSON：

```json
{
  "name": "Node.js 示例",
  "schedule": "*/5 * * * * *",
  "command": "node /scripts/hello.js",
  "working_dir": "/scripts",
  "timeout_sec": 300,
  "enabled": true
}
```

返回状态包含 `running`、`success`、`failed`、`timeout`、`interrupted`。日志以纯文本显示，不执行输出中的 HTML。

## 开发和验证

Go 1.24+，仅使用标准库，无第三方 Go 模块。目标系统为 Linux。

```sh
go test -race -count=1 ./...
go vet ./...
go build -o bin/lacron ./cmd/lacron
LACRON_CRON=false \
LACRON_DATA_DIR="$PWD/data" \
LACRON_SOCKET="$PWD/data/runner.sock" \
./bin/lacron
```

开发模式不调度 cron，但可使用 API/WebUI 手动运行任务。将任务工作目录改为本机存在的绝对路径。非 root 本地运行时，用户命令使用当前用户。

测试覆盖五/六字段数字语法、秒级预览与触发、日/星期 OR、时区和夏令时、旧 crontab 迁移、持久化、保存失败保持旧配置、即时编辑/暂停、触发去重、鉴权和跨站请求、进程组超时、防重入、并发上限、日志截断和重启恢复。

容器集成测试验证连续两次每 2 秒的真实调度、普通用户 Node.js 与 sh 脚本执行、启动与运行时 apk 安装、令牌隔离、暂停后手动运行、进程组超时、重启持久化和正常关闭：

```sh
docker build --pull -t lacron:test .
python3 tests/smoke.py lacron:test
python3 tests/smoke.py lacron:test --read-only
```

CI 还使用 Chromium 验证登录、任务增删改、预设、启停、手动运行、纯文本日志、搜索、主题持久化、移动端导航和退出登录，并上传浅色、深色和移动端截图作为检查记录。Playwright 仅用于测试，不进入运行镜像，也不需要前端构建。

## 协议

[MIT](LICENSE) © 2026 dismay712。基础镜像、Node.js 和系统软件包保留各自的开源协议。
