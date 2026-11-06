# lacron

基于 Alpine 的轻量定时任务容器：Go 内置秒级 cron 调度器、管理 API 和 WebUI，支持 Node.js LTS 与 sh 脚本，可自行安装 Alpine 软件包。

## 功能

- 简洁扁平化中文界面，支持浅色、深色和跟随系统，适配手机。
- 创建、编辑、复制、删除、启用和暂停任务，支持时间预设、搜索和自定义排序。
- 六字段秒级调度，兼容原有五字段分钟级任务，界面时间显示到秒。
- 执行 Shell / JavaScript 命令；暂停的任务也可手动运行。
- 执行记录按任务和状态分页筛选，运行中每 5 秒读取新增输出，支持停止执行和预览后清理日志。
- 系统设置支持在线修改总并发数、执行记录保留上限、单次日志大小和 Passkey 登录。
- 使用内置 SQLite 持久化任务、执行记录、变量和设置，按记录更新。任务各自使用非 root UID/GID。
- 变量值使用 AES-256-GCM 加密保存，仅向已授权任务注入。不同账号可使用同名变量。
- WebUI 支持管理令牌和 Passkey 登录，管理 API 支持 Bearer 令牌。
- 内置 `fetch` 多步骤 Cookie 签到示例：查状态、获取参数、提交签到、再次确认结果。
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

这个例子每 5 秒执行一次。Shell 示例命令：`sh /scripts/hello.sh`，也可以直接填写多行 sh 命令。将自己的脚本放到仓库的 `scripts/` 目录即可，Compose 将它只读挂载到容器 `/scripts`；这个挂载不影响根文件系统安装软件包。脚本文件及父目录必须允许对应任务 UID 读取、遍历（普通脚本可使用文件 `0644`、目录 `0755`）。需要第三方 npm 包时，可在自定义镜像构建期间安装到脚本项目中。

Compose 默认时区为 `Asia/Shanghai`，仅绑定宿主机 `127.0.0.1:8080`。远程访问可使用 SSH 端口转发或配置 HTTPS 反向代理。代理应保留 `Host` 和 `Authorization` 请求头。管理令牌有执行容器内命令的权限，请只交给可信管理员。

可复制 `.env.example` 为 `.env` 调整端口、时区、初始并发数和令牌。自定义令牌至少 24 字节且无空白。WebUI 登录后使用 12 小时有效的 HttpOnly 会话 Cookie。退出登录或重启服务会使会话失效。

## 系统设置与 Passkey

在「系统设置」可调整并发上限（1–32）、执行记录上限（1–2000）和单次日志上限（1–4096 KiB）。降低并发上限会限制后续执行的准入，当前任务可继续运行。降低记录上限会清理最早的已结束记录和对应日志。单次日志上限应用于设置保存后开始的执行。

1. 使用管理令牌登录。
2. 在「系统设置」启用 Passkey，填写浏览器使用的站点地址并保存。远程访问使用 HTTPS 域名，本机可以使用 `http://localhost:8080`。反向代理需保留浏览器请求的 `Host`。
3. 从该地址打开页面，点击「绑定 Passkey」并完成设备验证。之后可在登录页使用 Passkey，也可用管理令牌登录。
4. 可绑定多个凭据。移除凭据会撤销用它建立的会话。更换站点域名前，先移除该域名的凭据。

任务列表可按创建顺序、名称、最近执行、下次执行和状态升降序排列，排序偏好保存在当前浏览器。复制的任务默认暂停，并分配独立运行身份。如需让副本读取环境变量，可在变量页面重新授权。点击「停止执行」会终止本次运行的进程组，任务后续仍按计划调度。

## 环境变量与按任务授权

添加变量前，先用 `openssl rand -base64 32` 生成一次密钥，并将输出设为容器的 `LACRON_ENCRYPTION_KEY`。Compose 可在 `.env` 中设置，Podman 可使用 `-e LACRON_ENCRYPTION_KEY` 传入同名宿主机环境变量。重建容器时使用原密钥和数据卷。

侧栏和手机导航中的「环境变量」页面可新建、编辑、删除变量，并勾选允许读取它的任务。新变量默认不授权任何任务。执行时，Go 在启动进程前取一份已授权变量快照，追加到该任务独立的 `cmd.Env`；不会修改服务全局环境，也不会自动继承容器的任意环境变量。

1. 先创建任务，例如「账号 A 签到」。
2. 打开「环境变量」，添加 `CHECKIN_COOKIE`，粘贴该账号的 Cookie，勾选「账号 A 签到」。
3. 添加脚本需要的其他配置变量，并分别授权。脚本通过 `process.env.CHECKIN_COOKIE` 读取；sh 则使用 `"$CHECKIN_COOKIE"`。
4. 编辑变量时，值留空表示保留原值，可以只修改描述或授权。API 更新省略 `value` 也会保留；显式传 `"value":""` 才会设置空值。

SQLite 只保存变量的加密值和授权元数据，API 只返回元数据。请单独保管密钥，恢复数据卷时需要同一密钥。轮换时设置新的 `LACRON_ENCRYPTION_KEY`，同时用 `LACRON_PREVIOUS_ENCRYPTION_KEY` 提供旧密钥。成功启动并完成轮换后，可在下一次重建时移除旧密钥。

**多账号无需复制脚本**：可分别创建两个名为 `CHECKIN_COOKIE` 的变量，描述为「账号 A」和「账号 B」，只给各自任务授权。一个任务最多获授权一个同名变量；同时勾选造成冲突时，保存会被拒绝。站点地址等非敏感配置则可用一条变量授权给多个任务。

授权单位是 **lacron 任务**，不是文件路径。同一任务启动的所有脚本、命令和子进程共享该任务的环境；同一个 `.js` 文件在不同任务中运行，可以获得不同变量。修改、撤销授权或删除变量从下一次运行生效，已经运行的进程保留启动快照。

每个任务拥有不同且持久化的 Linux UID/GID，避免共用用户时经 `/proc/<pid>/environ` 读取其他任务的环境。默认分配范围为 `10001:10001` 到 `10500:10500`；任务编辑窗口和 `GET /api/jobs` 可查看实际身份。同一容器运行期间不复用已删除任务的身份，防止遗留进程读取新任务的环境；累计分配满 500 个后，重启容器可释放已删除任务的身份，现存任务身份不变。脚本的可写目录需要允许该任务身份访问。Rootless Podman 仍使用 `--user 0:0`，并需有覆盖该范围的 subordinate UID/GID 映射。非 root 开发模式不允许保存变量；若数据中已有变量，也不允许以共享用户执行任务。

变量名只支持字母、数字和下划线，不能以数字开头；系统的 `PATH`、`HOME`、`USER`、`LOGNAME`、`LANG`、`TZ` 以及 `LACRON_` 前缀保留。最多 200 个变量，单值最多 16 KiB，单任务授权值合计最多 64 KiB。任务默认继承 `umask 077`，新建普通文件默认仅所有者可读写。脚本若主动输出或共享凭据，环境注入与 UID 隔离无法撤回这些输出；管理账号仍能修改所有任务及授权，因此只向可信管理员开放管理令牌。

## Cookie 自动签到示例

[`scripts/checkin.js`](scripts/checkin.js) 使用 Node.js 内置 `fetch`，不依赖 npm，不进行登录，只使用用户手动设置的 Cookie。它演示了跨多次请求传递 `userId`、CSRF token 和 nonce 的流程。

新建任务：

```text
名称：账号 A 签到
Cron：0 0 8 * * *
命令：node /scripts/checkin.js
工作目录：/scripts
超时：120 秒
```

然后在环境变量页面添加并授权：

| 变量名 | 示例或默认值 | 用途 |
| --- | --- | --- |
| `CHECKIN_COOKIE` | `session=用户自己的Cookie; other=value` | 必填，只授权给对应账号的任务 |
| `CHECKIN_BASE_URL` | `https://example.com` | 必填，替换为实际站点地址 |
| `CHECKIN_STATUS_PATH` | `/api/status` | 可选，状态接口 |
| `CHECKIN_PREPARE_PATH` | `/api/checkin/prepare` | 可选，准备接口 |
| `CHECKIN_SUBMIT_PATH` | `/api/checkin` | 可选，签到接口 |
| `CHECKIN_TIMEOUT_MS` | `15000` | 可选，单次请求超时，100–60000 毫秒 |

示例假设 API 使用下面的协议，**请按实际接口修改脚本的字段提取、请求体和成功判断**：

| 步骤 | 请求 | 处理示例响应 |
| --- | --- | --- |
| API1 | `GET /api/status` | `{code:0,data:{signed:false,userId:"u1",csrfToken:"..."}}`；已签到时直接结束 |
| API2 | `POST /api/checkin/prepare`，JSON `{userId}`，携带 `X-CSRF-Token` | 从 `{code:0,data:{nonce:"..."}}` 提取本次 nonce |
| API3 | `POST /api/checkin`，JSON `{userId,nonce}`，携带 `X-CSRF-Token` | 要求 `{code:0,data:{success:true}}` |
| 最终确认 | 再次请求状态接口 | 只有 `data.signed === true` 才记录「签到成功」 |

所有请求携带授权 Cookie。示例将地址限制在 `CHECKIN_BASE_URL` 的同源接口，不自动跟随重定向；不自动重试提交请求，避免重复操作。Cookie 失效、请求超时、HTTP/业务错误、响应不是 JSON 或最终状态未确认时，以退出码 1 结束，lacron 将记录失败。日志不输出 Cookie、CSRF token、nonce 或完整响应。

镜像内已包含示例；如自行将宿主机 `scripts/` 挂载到 `/scripts`，请将最新 `checkin.js` 也放入该目录。`podman -e CHECKIN_COOKIE=...` 设置的是服务环境，任务不会自动继承；请通过变量管理页面设置和授权。

## 镜像与版本更新

多阶段 Dockerfile 默认使用：

| 阶段 | 镜像 | 用途 |
| --- | --- | --- |
| 运行环境 | `alpine:latest` | 最新稳定 Alpine；包含 BusyBox `crond` |
| JavaScript | `node:lts-alpine` | 官方最新 LTS 及其最新补丁，包含 `node`、`npm`、`npx` |
| Go 编译 | `golang:alpine` | 编译静态 Go 二进制，前端资源直接嵌入 |

构建时检查 `process.release.lts` 非空。最终镜像基于 `alpine:latest`，安装 `libstdc++`、`libatomic`、CA 证书、时区数据及 `tini`。Node 二进制来自官方 musl 镜像，构建时移除本地符号和调试信息。默认镜像保留 `node`、`nodejs`、`npm`、`npx`、npm 自带依赖及许可文件。Node 编译头文件和裁剪工具留在构建阶段。

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

拉取和启动镜像。需要使用变量时，先设置宿主机的 `LACRON_ENCRYPTION_KEY`，由 `-e` 传入。

```sh
podman pull ghcr.io/dismay712/lacron:latest
podman run -d --name lacron \
  --user 0:0 \
  --restart=unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e TZ=Asia/Shanghai \
  -e LACRON_ENCRYPTION_KEY \
  -v lacron-data:/data \
  ghcr.io/dismay712/lacron:latest
podman exec lacron cat /data/admin.token
```

挂载自己的脚本时，可在镜像名前添加 `-v "$PWD/scripts:/scripts:ro,Z"`，并确保脚本允许对应任务的运行 UID 读取。

## 安装 Alpine 软件包

默认 Compose 与上述 Podman 命令使用**可写根文件系统和容器默认能力集**，管理员可以直接安装：

```sh
podman exec --user 0:0 lacron apk add --no-cache bash curl jq
# Docker Compose 对应命令：
docker compose exec --user 0:0 lacron apk add --no-cache bash curl jq
```

安装完成后，任务可使用 `sh /scripts/task.sh`、`bash /scripts/task.bash`、`curl`、`jq` 等命令。`/bin/sh` 默认为 Alpine BusyBox ash；需要 Bash 语法的脚本请安装 bash 并使用 `bash` 执行。任务以各自的非 root UID/GID 运行；装包由管理员执行。

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

签到示例等只使用 Node 内置模块的任务可以构建 `runtime` 目标，省去 npm 和 npx：

```sh
podman build --pull --target runtime -t lacron:runtime .
```

默认的 `standard` 目标适合需要安装第三方模块的任务。构建带脚本的派生镜像时，可以用 `package-lock.json` 固定依赖，在同一层安装生产依赖并清理 npm 缓存：

```dockerfile
FROM ghcr.io/dismay712/lacron:latest
WORKDIR /scripts
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --cache /tmp/npm-cache \
    && rm -rf /tmp/npm-cache
COPY my-task.js ./
```

需要编译原生扩展时，请在构建阶段使用与目标运行时版本匹配的 Node 头文件。需要本地符号进行原生调试时，使用同版本的官方 Node 二进制。

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

- `/data/lacron.sqlite3` 保存任务、执行记录、加密变量、Passkey 凭据和系统设置。使用 WAL，每次执行只写新增或变更的记录。
- `/data/logs/` 保存每次执行的输出，默认保留 200 条记录，每次最多 256 KiB。淘汰执行记录时一并删除其日志。
- `/data/admin.token` 保存自动生成的管理令牌。
- `/run/lacron/runner.sock` 提供兼容的本地 `lacron trigger` 接口。

「执行记录」可按单次执行、任务或日期预览清理范围。可以清除输出并保留状态和耗时，也可以删除整条记录。清理跳过正在执行的任务。

从旧版升级时，保留原数据卷。服务首次启动会导入原有 `jobs.json`、`runs.json`、`variables.json` 和任务授权，现有日志继续使用。已有变量的实例必须先设置加密密钥。加密导入完成后会删除旧的明文 `variables.json`，原任务和执行 JSON 保留为升级前快照。旧 crontab 会在升级时清理。若要回退旧版，请使用升级前的数据卷备份。

通过命名卷 `lacron-data` 持久化，`docker compose down` 不删除数据；`down -v` 会删除数据。备份前停止容器，完整备份数据卷；恢复时保留文件权限。通过 WebUI/API 管理任务。单个数据目录只允许一个服务实例，使用文件锁防止多实例并发写入。

主服务默认以容器内 root 运行，以便启动时装包并在执行任务时切换到每个任务独立的普通 UID/GID；使用 `tini` 回收孤儿进程。Compose 默认保留容器默认能力集，支持管理员装包；未使用 privileged。升级时会给已有任务分配并保存不同的运行身份；原来只允许 UID 10001 写入的工作目录需要按任务实际 UID 调整。非 root 本地开发仅支持没有环境变量配置的任务，脚本使用当前用户。

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
| `LACRON_MAX_CONCURRENT` | `4` | 初始并发数，1–32。系统设置保存后以数据库中的值为准 |
| `LACRON_ENCRYPTION_KEY` | 空 | Base64 编码的 32 字节密钥。添加变量或迁移已有变量时必填 |
| `LACRON_PREVIOUS_ENCRYPTION_KEY` | 空 | 轮换加密密钥时临时提供的旧密钥 |
| `LACRON_APK_PACKAGES` | 空 | 启动时安装的 Alpine 软件包，以空白分隔；需 root 和可写根文件系统 |
| `LACRON_RUN_UID` / `LACRON_RUN_GID` | `10001` / `10001` | 隔离任务 UID/GID 范围的起始值，需容纳连续 500 个身份 |
| `LACRON_SCRIPT_PATH` | `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` | 用户命令的 PATH |
| `LACRON_CRON` | `true` | 是否启用 Go 秒级调度器；关闭后仍可手动执行 |

如果在 Compose 中使用 `LACRON_TOKEN_FILE`，请在自己的 Compose override 中添加只读 secret 挂载及该环境变量；保留 `LACRON_TOKEN` 为空。任务的默认超时为 300 秒，可逐个设为 1–86400 秒。

## API

管理 API 可用 `Authorization: Bearer <token>`，WebUI 使用同站会话 Cookie。`GET /healthz` 和浏览器登录入口允许未登录访问。`/healthz` 检查调度器状态，`/api/system` 返回 `scheduler: "go"`、`schedule_precision: "second"`、原有的 `cron_enabled` / `cron_running` 及当前运行上限。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/system` | 版本、时区和 cron 状态 |
| GET / POST | `/api/jobs` | 查询 / 新建任务 |
| PUT / DELETE | `/api/jobs/{id}` | 更新 / 删除任务 |
| POST | `/api/jobs/{id}/run` | 手动执行，返回 202 和执行 ID |
| POST | `/api/jobs/{id}/copy` | 创建暂停的副本，支持可选的 `name` |
| POST | `/api/runs/{id}/stop` | 停止指定执行，返回 202 |
| GET | `/api/runs?job_id={id}` | 最近执行，兼容原有数组格式 |
| GET | `/api/runs?limit=50&cursor=…&status=…` | 分页和状态过滤，返回 `items` 和下一页游标 |
| GET | `/api/runs/{id}/log` | 完整日志，返回 `output` |
| GET | `/api/runs/{id}/log?offset=0&limit=65536` | 分块日志，返回运行状态、输出、`next_offset`、`more` 和 `reset` |
| POST | `/api/runs/cleanup/preview` 和 `/api/runs/cleanup` | 预览和确认清理，提交预览返回的 `cutoff` |
| GET | `/api/changes` | 查询各数据区的修订号和执行统计 |
| GET / PUT | `/api/settings` | 查看和保存系统设置 |
| GET / DELETE | `/api/passkeys`、`/api/passkeys/{id}` | 查看已绑定设备和移除凭据 |
| GET / POST | `/api/variables` | 查询元数据 / 新建变量；不回显值 |
| PUT / DELETE | `/api/variables/{id}` | 修改值/描述/任务授权，或删除变量 |

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

返回状态包含 `running`、`success`、`failed`、`timeout`、`cancelled`、`interrupted`。日志以纯文本显示，不执行输出中的 HTML。

创建变量使用 `{"name":"CHECKIN_COOKIE","value":"用户Cookie","description":"账号 A","job_ids":["任务ID"]}`。更新时省略 `value` 保留原值；省略 `job_ids` 表示撤销全部授权。变量 API 的响应只包含 `id`、`name`、`description`、`job_ids`、`has_value` 和时间戳。创建/更新变量需要服务运行于容器内 root，普通非 root 开发模式返回 403。

## 开发和验证

目标系统为 Linux，构建需要 Go 1.26 或更新版本。SQLite 和 WebAuthn 依赖随 Go 二进制构建。

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
python3 tests/variables.py lacron:test
node --test tests/checkin.test.cjs
```

变量单元测试验证授权隔离、身份迁移、明文导入、SQLite 加密存储和密钥轮换。容器测试还实际检查不同 UID 的任务无法读取对方的 `/proc/<pid>/environ`、变量存储文件或未授权值，并验证定时执行和重启后的授权。签到示例通过本地模拟 API 验证真实 fetch 链路、过期 Cookie、超时、重定向拒绝和最终状态确认，不访问真实账号。

CI 还使用 Chromium 验证任务和变量管理、排序、复制、停止执行、增量日志、清理、系统设置、Passkey 注册与登录和会话撤销，并上传浅色、深色和移动端截图。Playwright 仅用于测试，不进入运行镜像，也不需要前端构建。

## 协议

[MIT](LICENSE) © 2026 dismay712。基础镜像、Node.js 和系统软件包保留各自的开源协议。
