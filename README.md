# lacron

基于 Alpine 的轻量定时任务容器：BusyBox cron 负责调度，Go 提供管理 API 和 WebUI，Node.js LTS 执行 JavaScript 脚本。

## 功能

- 简洁扁平化中文界面，支持浅色、深色和跟随系统，适配手机。
- 创建、编辑、删除、启用和暂停 cron 任务，支持常用时间预设、搜索和下次执行时间。
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
Cron：*/5 * * * *
命令：node /scripts/hello.js
工作目录：/scripts
超时：300 秒
```

Shell 示例命令：`sh /scripts/hello.sh`。将自己的脚本放到仓库的 `scripts/` 目录即可，Compose 将它只读挂载到容器 `/scripts`。脚本文件及父目录必须允许 UID 10001 读取、遍历。需要第三方 npm 包时，可在自定义镜像构建期间安装到脚本项目中。

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

使用标准五段式数字 cron：`分 时 日 月 星期`。

| 字段 | 范围 |
| --- | --- |
| 分 | 0–59 |
| 时 | 0–23 |
| 日 | 1–31 |
| 月 | 1–12 |
| 星期 | 0–6，0 = 周日 |

支持 `*`、逗号列表、递增范围和步长（例如 `*/5`、`1-5`、`1,15`、`1-20/2`）。不支持秒字段、月份/星期英文名、`7` 代表周日或 `@reboot` 等宏。

「日」和「星期」均受限时按 **OR** 匹配；选择整个范围等价于 `*`。后端将表达式展开后交给 BusyBox，使日期步长（例如 `*/2` 从 1 日开始）与界面计算一致。不存在的日期（例如 2 月 31 日）不执行，界面会提示未来 8 年内无匹配时间。

时区由 `TZ` 决定，界面也按该时区显示时间。调度精度为分钟，修改最迟在下一个分钟边界生效。暂停或修改后的过期 cron 触发会被后端再次核对并拒绝。容器停止期间的任务不补跑；夏令时及系统时间跳变遵循 BusyBox crond 的行为。下一次时间为日历预览，时钟跳变时实际触发可能有所不同。

同一任务运行期间会跳过重复触发；达到全局并发上限时也会跳过，不排队。编辑运行中的任务只影响后续执行；任务正在运行时无法删除。手动执行可用于暂停的任务。

## 持久化与运行权限

- `/data/jobs.json`：任务配置。
- `/data/crontabs/root`：自动生成的 crontab，用户命令不会直接写入此文件。
- `/data/runs.json`、`/data/logs/`：全局最近 200 次执行及日志，每次输出上限 256 KiB；超过上限继续消费输出并丢弃，防止日志填满磁盘。被淘汰记录的日志一并删除。
- `/data/admin.token`：自动生成的管理令牌，只有 root 可读。
- `/run/lacron/runner.sock`：仅供 cron 本地触发的 Unix socket，不对外暴露。

通过命名卷 `lacron-data` 持久化，`docker compose down` 不删除数据；`down -v` 会删除数据。备份前停止容器，完整备份数据卷；恢复时保留文件权限。不要直接编辑自动生成的 crontab。单个数据目录只允许一个服务实例，使用文件锁防止多实例并发写入。

BusyBox crond 需要 root 并要求 crontab 文件归 root 所有，因此主服务作为监督进程运行，实际用户命令在启动时降至普通 UID/GID。Compose 默认只保留 `SETUID`、`SETGID`、`KILL` 能力（`KILL` 用于监督进程终止其他 UID 的超时任务），启用只读根文件系统和 `no-new-privileges`；使用 `tini` 回收孤儿进程。不要给任务挂载 Docker socket 或主机敏感目录。脚本具有容器网络访问能力，这不是面向不可信租户的代码沙箱。

工作目录默认为 `/scripts`。需要写文件时，应额外挂载可写目录（例如 `/work`）并赋予运行 UID/GID 写权限。`/tmp` 是容量 64 MiB 的临时空间，重建容器后不保留。服务重启时，上次未完成的执行会标记为「已中断」。

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
| `LACRON_RUN_UID` / `LACRON_RUN_GID` | `10001` / `10001` | 用户命令的非零 UID/GID |
| `LACRON_SCRIPT_PATH` | `/usr/local/bin:/usr/bin:/bin` | 用户命令的 PATH |
| `LACRON_CRON` | `true` | 本地开发时可关闭 crond |

如果在 Compose 中使用 `LACRON_TOKEN_FILE`，请在自己的 Compose override 中添加只读 secret 挂载及该环境变量；保留 `LACRON_TOKEN` 为空。任务的默认超时为 300 秒，可逐个设为 1–86400 秒。

## API

所有 `/api/*` 请求需要 `Authorization: Bearer <token>`，`GET /healthz` 无需鉴权。`/healthz` 同时检查 cron 监督状态；cron 意外退出会使服务退出，交由 Compose 重启。

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
  "schedule": "*/5 * * * *",
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

测试覆盖 cron 数字语法、日/星期 OR、时区和夏令时预览、crontab 注入隔离、持久化、保存失败回滚、鉴权和跨站请求、进程组超时、防重入、并发上限、日志截断和重启恢复。

容器集成测试会等待一次真实分钟边界，验证 cron → Go → 普通用户 Node.js 的整条链路，以及只读根文件系统、令牌隔离、暂停后手动运行、重启持久化和 cron 崩溃退出：

```sh
docker build --pull -t lacron:test .
python3 tests/smoke.py lacron:test
```

CI 还使用 Chromium 验证登录、任务增删改、预设、启停、手动运行、纯文本日志、搜索、主题持久化、移动端导航和退出登录，并上传浅色、深色和移动端截图作为检查记录。Playwright 仅用于测试，不进入运行镜像，也不需要前端构建。

## 协议

[MIT](LICENSE) © 2026 dismay712。基础镜像、Node.js 和系统软件包保留各自的开源协议。
