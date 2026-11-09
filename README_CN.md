# GPT-Load Lite

[English](README.md) | 中文 | [日本語](README_JP.md)

[![Releases](https://img.shields.io/badge/releases-GitHub-blue)](https://github.com/lll9p/gpt-load-lite/releases)
![Go Version](https://img.shields.io/badge/Go-1.25+-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

GPT-Load Lite 是使用 Go 开发的单实例 AI 接口透明代理，提供密钥管理、上游负载均衡、监控、动态配置、认证和 API 密钥加密功能。

Fork 来源：GPT-Load Lite 基于 [tbphp/gpt-load](https://github.com/tbphp/gpt-load)，采用 MIT 许可证；原版权声明见 [LICENSE](LICENSE)。

## 功能特性

- **密钥选择**：分组可选轮询（`round_robin`）或优先填满（`fill_first`）；fill-first 优先使用首个可用密钥，而非每次请求都轮换。
- **粘性出站代理**：HRW 将密钥映射到稳定代理 ID，故障时不会自动更换代理或回退直连。
- **透明代理**: 完全保留原生 API 格式，支持 OpenAI、Google Gemini 和 Anthropic Claude 等多种格式
- **智能密钥管理**: 高性能密钥池，支持分组管理、自动轮换和故障恢复
- **负载均衡**: 支持多上游端点的加权负载均衡，提升服务可用性
- **智能故障处理**: 自动密钥黑名单管理和恢复机制，确保服务连续性
- **动态配置**: 系统设置和分组配置支持热重载，无需重启即可生效
- **现代化管理**: 基于 Vue 3 的 Web 管理界面，直观易用
- **全面监控**: 实时统计、健康检查、详细请求日志
- **高性能设计**: 零拷贝流式传输、连接池复用、原子操作
- **生产就绪**: 优雅关闭、错误恢复、完善的安全机制
- **双重认证体系**: 管理端与代理端认证分离，代理认证支持全局和分组级别密钥

## 支持的 AI 服务

GPT-Load Lite 作为透明代理服务，完整保留各 AI 服务商的原生 API 格式：

- **OpenAI 格式**: 官方 OpenAI API、Azure OpenAI、以及其他 OpenAI 兼容服务
- **Google Gemini 格式**: Gemini Pro、Gemini Pro Vision 等模型的原生 API
- **Anthropic Claude 格式**: Claude 系列模型，支持高质量的对话和文本生成
- **Tavily REST**（`tavily`）：支持 POST `/search`、`/extract`、`/crawl`、`/map`、`/research` 和 GET `/research/{request_id}`；使用配置的代理密钥作为 Bearer token，客户端 API 密钥会替换为池中密钥。

## 快速开始

### 环境要求

- Go 1.25+（源码构建）
- Docker（容器化部署）
- SQLite（默认，无需独立数据库服务）或 PostgreSQL

**仅支持单实例：** GPT-Load Lite 不支持多个应用实例共享状态，即使使用 PostgreSQL 也不支持。待批量写入的日志和任务状态保存在内存中，不跨重启保留；异常退出可能丢失尚未落库的日志。

以下命令均在本地 GPT-Load Lite 源码根目录执行，请从当前源码构建。

> **从上游 v1.4.10 升级（SQLite / Docker Compose，未使用聚合分组）：** 先停止旧服务，备份整个数据目录、原 `.env` 和 Compose 配置。沿用同一 `DATABASE_DSN`、数据卷和 `ENCRYPTION_KEY`（原为空则仍留空），不要在本次升级中同时更换加密密钥。已有数据库会在启动时自动迁移，无需重新导入 Key。回退必须恢复升级前的数据库，不能只换回旧镜像。此说明不代表兼容其他上游版本。

### 方式一：Docker 快速开始

```bash
# Build the local source image
docker build -t gpt-load-lite:local .

docker run -d --name gpt-load \
    -p 3001:3001 \
    -e AUTH_KEY=your-secure-key-here \
    -v "$(pwd)/data":/app/data \
    gpt-load-lite:local
```

> 部署前请将 `your-secure-key-here` 改为强随机密钥。

### 方式二：使用 Docker Compose（推荐）

```bash
# Create configuration from the template in this source checkout
cp .env.example .env

# Edit .env and set AUTH_KEY to a strong, random key.
# Never use default or simple keys such as sk-123456.

# Build from local source and start the service
docker compose up -d --build
```

默认使用 SQLite。如需 PostgreSQL，请在 `docker-compose.yml` 中取消 PostgreSQL 服务及其 `depends_on` 项的注释，在 `.env` 中设置 `DATABASE_DSN`，并使用强数据库密码。使用 PostgreSQL 仍然只支持一个应用实例。

**其他命令：**

```bash
# Check service status
docker compose ps

# View logs
docker compose logs -f

# Restart the service
docker compose down && docker compose up -d

# Rebuild and deploy after updating your local Lite source
docker compose up -d --build
```

部署完成后：

- Web 管理界面： <http://localhost:3001>
- API 代理地址： <http://localhost:3001/proxy>

> 使用你配置的 `AUTH_KEY` 登录管理端。

### 方式三：源码构建

在本地 Lite 源码目录中使用 Go 1.25+。SQLite 无需独立数据库服务；使用 PostgreSQL 时需有可连接的 PostgreSQL 服务。

```bash
# Create configuration
cp .env.example .env

# Edit .env and set AUTH_KEY to a strong, random key.
# Leave DATABASE_DSN empty for SQLite, or set a PostgreSQL DSN.

# Run
make run
```

部署完成后：

- Web 管理界面： <http://localhost:3001>
- API 代理地址： <http://localhost:3001/proxy>

> 使用你配置的 `AUTH_KEY` 登录管理端。

## 配置系统

### 配置架构概述

GPT-Load Lite 采用双层配置架构：

#### 1. 静态配置（环境变量）

- **特点**：应用启动时读取，运行期间不可修改，需重启应用生效
- **用途**：基础设施配置，如数据库连接、服务器端口、认证密钥等
- **管理方式**：通过 `.env` 文件或系统环境变量设置

#### 2. 动态配置（热重载）

- **系统设置**：存储在数据库中，为整个应用提供统一的行为基准
- **分组配置**：为特定分组定制的行为参数，可覆盖系统设置
- **配置优先级**：分组配置 > 系统设置 > 环境配置
- **特点**：支持热重载，修改后立即生效，无需重启应用

<details>
<summary>静态配置（环境变量）</summary>

**服务器配置：**

| 配置项       | 环境变量                           | 默认值          | 说明                       |
| ------------ | ---------------------------------- | --------------- | -------------------------- |
| 服务端口     | `PORT`                             | 3001            | HTTP 服务器监听端口        |
| 服务地址     | `HOST`                             | 0.0.0.0         | HTTP 服务器绑定地址        |
| 读取超时     | `SERVER_READ_TIMEOUT`              | 60              | HTTP 服务器读取超时（秒）  |
| 写入超时     | `SERVER_WRITE_TIMEOUT`             | 600             | HTTP 服务器写入超时（秒）  |
| 空闲超时     | `SERVER_IDLE_TIMEOUT`              | 120             | HTTP 连接空闲超时（秒）    |
| 优雅关闭超时 | `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT` | 10              | 服务优雅关闭等待时间（秒） |
| 时区         | `TZ`                               | `Asia/Shanghai` | 指定时区                   |

**安全配置：**

| 配置项   | 环境变量         | 默认值 | 说明                                                                               |
| -------- | ---------------- | ------ | ---------------------------------------------------------------------------------- |
| 管理密钥 | `AUTH_KEY`       | -      | **管理端**的访问认证密钥，请修改为强密码                                           |
| 加密密钥 | `ENCRYPTION_KEY` | -      | 加密存储的API密钥，支持任意字符串或留空禁用加密。参见[数据加密迁移](#数据加密迁移) |

**数据库配置：**

| 配置项     | 环境变量       | 默认值             | 说明                                 |
| ---------- | -------------- | ------------------ | ------------------------------------ |
| 数据库连接 | `DATABASE_DSN` | ./data/gpt-load.db | 数据库连接字符串 (DSN) 或文件路径    |

**性能与跨域配置：**

| 配置项       | 环境变量                  | 默认值                        | 说明                     |
| ------------ | ------------------------- | ----------------------------- | ------------------------ |
| 最大并发请求 | `MAX_CONCURRENT_REQUESTS` | 100                           | 系统允许的最大并发请求数 |
| 启用 CORS    | `ENABLE_CORS`             | false                         | 是否启用跨域资源共享     |
| 允许的来源   | `ALLOWED_ORIGINS`         | -                             | 允许的来源，逗号分隔     |
| 允许的方法   | `ALLOWED_METHODS`         | `GET,POST,PUT,DELETE,OPTIONS` | 允许的 HTTP 方法         |
| 允许的头部   | `ALLOWED_HEADERS`         | `*`                           | 允许的请求头，逗号分隔   |
| 允许凭据     | `ALLOW_CREDENTIALS`       | false                         | 是否允许发送凭据         |

**日志配置：**

| 配置项       | 环境变量          | 默认值                | 说明                               |
| ------------ | ----------------- | --------------------- | ---------------------------------- |
| 日志级别     | `LOG_LEVEL`       | `info`                | 日志级别：debug, info, warn, error |
| 日志格式     | `LOG_FORMAT`      | `text`                | 日志格式：text, json               |
| 启用文件日志 | `LOG_ENABLE_FILE` | false                 | 是否启用文件日志输出               |
| 日志文件路径 | `LOG_FILE_PATH`   | `./data/logs/app.log` | 日志文件存储路径                   |

**代理配置：**

GPT-Load Lite 会自动从环境变量中读取代理设置，用于向上游 AI 服务商发起请求。

| 配置项     | 环境变量      | 默认值 | 说明                                     |
| ---------- | ------------- | ------ | ---------------------------------------- |
| HTTP 代理  | `HTTP_PROXY`  | -      | 用于 HTTP 请求的代理服务器地址           |
| HTTPS 代理 | `HTTPS_PROXY` | -      | 用于 HTTPS 请求的代理服务器地址          |
| 无代理     | `NO_PROXY`    | -      | 不需要通过代理访问的主机或域名，逗号分隔 |

支持的代理协议格式：

- **HTTP**: `http://user:pass@host:port`
- **HTTPS**: `https://user:pass@host:port`
- **SOCKS5**: `socks5://user:pass@host:port`
</details>

<details>
<summary>动态配置（热重载）</summary>

**基础设置：**

| 配置项       | 字段名                               | 默认值                      | 分组可覆盖 | 说明                                                           |
| ------------ | ------------------------------------ | --------------------------- | ---------- | -------------------------------------------------------------- |
| 项目地址     | `app_url`                            | `http://localhost:3001`     | ❌         | 项目基础 URL                                                   |
| 全局代理密钥 | `proxy_keys`                         | 初始值为环境配置的 AUTH_KEY | ❌         | 全局生效的代理认证密钥，多个用逗号分隔                         |
| 日志保留天数 | `request_log_retention_days`         | 7                           | ❌         | 请求日志保留天数，0 为不清理                                   |
| 日志写入间隔 | `request_log_write_interval_minutes` | 1                           | ❌         | 日志写入数据库周期（分钟）                                     |
| 启用日志详情 | `enable_request_body_logging`        | false                       | ✅         | 是否在请求日志中记录完整的请求体内容，启用会增加内存和存储占用 |

**请求设置：**

| 配置项               | 字段名                    | 默认值 | 分组可覆盖 | 说明                                               |
| -------------------- | ------------------------- | ------ | ---------- | -------------------------------------------------- |
| 请求超时             | `request_timeout`         | 600    | ✅         | 转发请求完整生命周期超时（秒）                     |
| 连接超时             | `connect_timeout`         | 15     | ✅         | 与上游服务建立连接超时（秒）                       |
| 空闲连接超时         | `idle_conn_timeout`       | 120    | ✅         | HTTP 客户端空闲连接超时（秒）                      |
| 响应头超时           | `response_header_timeout` | 600    | ✅         | 等待上游响应头超时（秒）                           |
| 最大空闲连接数       | `max_idle_conns`          | 100    | ✅         | 连接池最大空闲连接总数                             |
| 每主机最大空闲连接数 | `max_idle_conns_per_host` | 50     | ✅         | 每个上游主机最大空闲连接数                         |
| 代理服务器地址       | `proxy_url`               | -      | ✅         | 用于转发请求的 HTTP/HTTPS 代理，为空则使用环境配置 |

**密钥配置：**

| 配置项         | 字段名                            | 默认值 | 分组可覆盖 | 说明                                             |
| -------------- | --------------------------------- | ------ | ---------- | ------------------------------------------------ |
| 最大重试次数   | `max_retries`                     | 3      | ✅         | 单个请求使用不同密钥的最大重试次数               |
| 黑名单阈值     | `blacklist_threshold`             | 3      | ✅         | 密钥累计失败多少次后进入黑名单                   |
| 密钥验证间隔   | `key_validation_interval_minutes` | 60     | ✅         | 后台定时验证密钥周期（分钟）                     |
| 密钥验证并发数 | `key_validation_concurrency`      | 10     | ✅         | 后台定时验证无效 Key 时的并发数                  |
| 密钥验证超时   | `key_validation_timeout_seconds`  | 20     | ✅         | 后台定时验证单个 Key 时的 API 请求超时时间（秒） |

</details>

### 粘性出站代理

在设置页管理全局出站代理列表，再为分组开启「粘性出站代理」。支持完整 http/https/socks5/socks5h URL，用户名和密码须 URL 编码。列表脱敏凭据，密码编辑框可显式查看；Key 卡片显示当前代理名。

HRW 使用 KeyHash 和稳定代理 ID，无 TTL、无绑定表；跨开启组同一密钥映射相同，多密钥可共用代理。顺序和重启不改变映射；增删、禁用、恢复代理或加密迁移导致 KeyHash 改变，可能重新映射部分密钥。网络故障不会自动换代理或直连，无启用代理时请求失败。保存的固定 `proxy_url` 和 `HTTP_PROXY` 仅在关闭粘性代理时适用。固定 URL 不等于固定出口 IP。代理凭据纳入 `migrate-keys`，迁移加密密钥时须一并考虑。

## 数据加密迁移

GPT-Load Lite 支持对 API 密钥进行加密存储。您可以随时启用、禁用或更换加密密钥。

<details>
<summary>查看数据加密迁移详细说明</summary>

### 迁移场景

- **启用加密**：将明文数据加密存储 - 使用 `--to <新密钥>`
- **禁用加密**：将加密数据解密为明文 - 使用 `--from <当前密钥>`
- **更换密钥**：更换加密密钥 - 使用 `--from <当前密钥> --to <新密钥>`

### 操作步骤

#### Docker Compose 部署

```bash
# 1. Build the image from your local Lite source
docker compose build

# 2. 停止服务
docker compose down

# 3. 备份数据库（强烈建议）
# 执行迁移前，必须手动备份数据库或者导出你的密钥，避免因操作或者异常导致的密钥丢失。

# 4. 执行迁移命令
# 启用加密（your-32-char-secret-key 为你的密钥，建议使用32位以上的随机字符串）
docker compose run --rm gpt-load migrate-keys --to "your-32-char-secret-key"

# 禁用加密
docker compose run --rm gpt-load migrate-keys --from "your-current-key"

# 更换密钥
docker compose run --rm gpt-load migrate-keys --from "old-key" --to "new-32-char-secret-key"

# 5. 更新配置文件
# 编辑 .env 文件，设置 ENCRYPTION_KEY 与 --to 参数一致
# 如果禁用加密，则删除 ENCRYPTION_KEY 或设置为空
vim .env
# 添加或修改: ENCRYPTION_KEY=your-32-char-secret-key

# 6. 重启服务
docker compose up -d
```

#### 源码构建部署

```bash
# 1. 停止服务
# 停止正在运行的服务进程（Ctrl+C 或 kill 进程）

# 2. 备份数据库（强烈建议）
# 执行迁移前，必须手动备份数据库或者导出你的密钥，避免因操作或者异常导致的密钥丢失。

# 3. 执行迁移命令
# 启用加密
make migrate-keys ARGS="--to your-32-char-secret-key"

# 禁用加密
make migrate-keys ARGS="--from your-current-key"

# 更换密钥
make migrate-keys ARGS="--from old-key --to new-32-char-secret-key"

# 4. 更新配置文件
# 编辑 .env 文件，设置 ENCRYPTION_KEY 与 --to 参数一致
echo "ENCRYPTION_KEY=your-32-char-secret-key" >> .env

# 5. 重启服务
make run
```

### 注意事项

⚠️ **重要提醒**：

- **ENCRYPTION_KEY 一旦丢失将无法恢复已加密的数据！** 请务必安全备份此密钥，建议使用密码管理器或安全的密钥管理系统保存
- 迁移前**必须停止服务**，避免数据不一致
- 强烈建议**备份数据库**，以防迁移失败需要恢复
- 密钥建议使用 **32 位或更长的随机字符串**，确保安全性
- 迁移后确保 `.env` 中的 `ENCRYPTION_KEY` 与 `--to` 参数一致
- 如果禁用加密，需要删除或清空 `ENCRYPTION_KEY` 配置

### 密钥生成示例

```bash
# 生成安全的随机密钥（32字符）
openssl rand -base64 32 | tr -d "=+/" | cut -c1-32
```

</details>

## Web 管理界面

访问管理控制台：<http://localhost:3001>（默认地址）

Web 管理界面提供以下功能：

- **仪表盘**: 实时统计信息和系统状态概览
- **密钥管理**: 创建和配置 AI 服务商分组，添加、删除和监控 API 密钥
- **请求日志**: 详细的请求历史记录和调试信息
- **系统设置**: 全局配置管理和热重载

## API 使用说明

<details>
<summary>代理接口调用方式</summary>

GPT-Load Lite 通过分组名称路由请求到不同的 AI 服务。使用方式如下：

### 1. 代理端点格式

```text
http://localhost:3001/proxy/{group_name}/{原始API路径}
```

- `{group_name}`: 在管理界面创建的分组名称
- `{原始API路径}`: 保持与原始 AI 服务完全一致的路径

### 2. 认证方式

在 Web 管理界面中配置**代理密钥** (`Proxy Keys`)，可设置系统级别和分组级别的代理密钥。

- **认证方式**: 与原生 API 一致，但需将原始密钥替换为配置的代理密钥。
- **密钥作用域**: 在系统设置配置的 **全局代理密钥** 可以在所有分组使用，在分组配置的 **分组代理密钥** 仅在当前分组有效。
- **格式**: 多个密钥使用半角英文逗号分隔。

### 3. OpenAI 接口调用示例

GPT-Load Lite 当前支持两种 OpenAI 兼容分组类型：

- `openai`（OpenAI Chat Completions 格式）
- `openai-response`（OpenAI Responses 格式）

假设创建了名为 `openai` 的分组：

**原始调用方式：**

```bash
curl -X POST https://api.openai.com/v1/chat/completions \
  -H "Authorization: Bearer sk-your-openai-key" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4.1-mini", "messages": [{"role": "user", "content": "Hello"}]}'
```

**代理调用方式：**

```bash
curl -X POST http://localhost:3001/proxy/openai/v1/chat/completions \
  -H "Authorization: Bearer your-proxy-key" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4.1-mini", "messages": [{"role": "user", "content": "Hello"}]}'
```

**变更说明：**

- 将 `https://api.openai.com` 替换为 `http://localhost:3001/proxy/openai`
- 将原始 API Key 替换为**代理密钥**

**OpenAI Responses 格式示例（`openai-response` 分组）：**

```bash
curl -X POST http://localhost:3001/proxy/openai-response/v1/responses \
  -H "Authorization: Bearer your-proxy-key" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4.1-mini", "input": "Hello"}'
```

### 4. Gemini 接口调用示例

假设创建了名为 `gemini` 的分组：

**原始调用方式：**

```bash
curl -X POST https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:generateContent?key=your-gemini-key \
  -H "Content-Type: application/json" \
  -d '{"contents": [{"parts": [{"text": "Hello"}]}]}'
```

**代理调用方式：**

```bash
curl -X POST http://localhost:3001/proxy/gemini/v1beta/models/gemini-2.5-pro:generateContent?key=your-proxy-key \
  -H "Content-Type: application/json" \
  -d '{"contents": [{"parts": [{"text": "Hello"}]}]}'
```

**变更说明：**

- 将 `https://generativelanguage.googleapis.com` 替换为 `http://localhost:3001/proxy/gemini`
- 将 URL 参数中的 `key=your-gemini-key` 替换为**代理密钥**

### 5. Anthropic 接口调用示例

假设创建了名为 `anthropic` 的分组：

**原始调用方式：**

```bash
curl -X POST https://api.anthropic.com/v1/messages \
  -H "x-api-key: sk-ant-api03-your-anthropic-key" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-sonnet-4-20250514", "messages": [{"role": "user", "content": "Hello"}]}'
```

**代理调用方式：**

```bash
curl -X POST http://localhost:3001/proxy/anthropic/v1/messages \
  -H "x-api-key: your-proxy-key" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-sonnet-4-20250514", "messages": [{"role": "user", "content": "Hello"}]}'
```

**变更说明：**

- 将 `https://api.anthropic.com` 替换为 `http://localhost:3001/proxy/anthropic`
- 将 `x-api-key` 头部中的原始 API Key 替换为**代理密钥**

### 6. 支持的接口

**OpenAI Chat Completions 格式（`openai`）：**

- `/v1/chat/completions` - 聊天对话
- `/v1/completions` - 文本补全
- `/v1/embeddings` - 文本嵌入
- `/v1/models` - 模型列表
- 以及其他所有 OpenAI 兼容接口

**OpenAI Responses 格式（`openai-response`）：**

- `/v1/responses` - 统一响应生成
- `/v1/models` - 模型列表
- 以及其他所有 OpenAI Responses 兼容接口

**Gemini 格式：**

- `/v1beta/models/*/generateContent` - 内容生成
- `/v1beta/models` - 模型列表
- 以及其他所有 Gemini 原生接口

**Anthropic 格式：**

- `/v1/messages` - 消息对话
- `/v1/models` - 模型列表（如果可用）
- 以及其他所有 Anthropic 原生接口

### 7. 客户端 SDK 配置

**OpenAI Python SDK：**

```python
from openai import OpenAI

client = OpenAI(
    api_key="your-proxy-key",  # 使用密钥
    base_url="http://localhost:3001/proxy/openai"  # 使用代理端点
)

response = client.chat.completions.create(
    model="gpt-4.1-mini",
    messages=[{"role": "user", "content": "Hello"}]
)
```

**Google Gemini SDK (Python)：**

```python
import google.generativeai as genai

# 配置 API 密钥和基础 URL
genai.configure(
    api_key="your-proxy-key",  # 使用代理密钥
    client_options={"api_endpoint": "http://localhost:3001/proxy/gemini"}
)

model = genai.GenerativeModel('gemini-2.5-pro')
response = model.generate_content("Hello")
```

**Anthropic SDK (Python)：**

```python
from anthropic import Anthropic

client = Anthropic(
    api_key="your-proxy-key",  # 使用代理密钥
    base_url="http://localhost:3001/proxy/anthropic"  # 使用代理端点
)

response = client.messages.create(
    model="claude-sonnet-4-20250514",
    messages=[{"role": "user", "content": "Hello"}]
)
```

> **重要提示**：作为透明代理服务，GPT-Load Lite 完全保留各 AI 服务的原生 API 格式和认证方式，仅需要替换端点地址并使用在管理端配置的**代理密钥**即可无缝迁移。

</details>

## 许可证

MIT 许可证 - 详情请参阅 [LICENSE](LICENSE) 文件。

## 升级说明

聚合分组入口及父聚合日志字段已退役，普通分组、API 密钥和实际请求记录保留，父组重复统计会被清理。升级旧库前请备份数据库。若聚合组自身挂有密钥，迁移会停止：请先导出密钥并迁入普通分组。旧客户端需改用普通分组入口；回退需要数据库备份。
