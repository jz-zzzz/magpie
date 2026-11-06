# 白标二开计划：品牌可远程配置的桌面助手

> 目标：基于 magpie 二开一个「你自己的品牌 + 你自己的中转站」桌面客户端。
> 核心原则：**代码里不写死任何业务信息**（品牌名、域名、API 路径、更新地址、中转站线路），全部由远程配置下发；业务调整不发版，改服务器上的 JSON 即可。
> 同时保证**与上游 magpie 主线可长期合并**。

---

## 1. 参考产品分析

参考对象是 Geek2API 桌面助手（Wails v3 + Go，与 magpie 同技术栈），其公开演示暴露了完整后端接口（共 66 个方法）：

| 能力域 | 接口 |
| --- | --- |
| 账号 | `Login` `Register` `Logout` `GetSession` `GetAuthOptions` `GetCaptchaConfig` `SendVerifyCode` `StartOAuthLogin` `PollOAuthLogin` `CancelOAuthLogin` |
| Key | `ListKeys` `ListGroups` `CreateAPIKey` `DetectConfiguredKey` |
| 工具配置 | `GetTools` `ConfigureTool` `RemoveTool` `InstallTool` `GetInstallPlan` `LaunchTool` `SetToolModel` `SetToolOrder` `CodexBlockingProcesses` `CloseCodexProcesses` |
| 本地路由 | `GetRouterStatus` `SetRouterEnabled` `GetRouterConfig` `SetRouterConfig` `GetNetworkConfig` `SetProxyConfig` `GetLines` `SetActiveLine` `ActiveBaseURL` `GetUsageStats` `GetLineHealth` `ClearUsage` |
| MCP / 提示词 / Skills / 会话 | `ListMCPServers` `UpsertMCPServer` `DeleteMCPServer` `SetMCPEnabled` `ImportMCPServers`；`ListPrompts` `UpsertPrompt` `DeletePrompt` `SetPromptEnabled` `ImportPrompts`；`ListSkills` `SetSkillEnabled` `InstallSkill` `UninstallSkill` `ImportSkills`；`ListSessions` `DeleteSession` |

magpie 已有的底层能力（无需重造）：agent 配置写入（带标记区块、自动备份、一键还原，[internal/agent](internal/agent)）、本地网关（多线路 failover、多 Key 轮换、模型别名、限流预算、用量统计、协议转换、局域网共享，[internal/gateway](internal/gateway)）、MCP 路由、提示词库、Skills、会话历史。

**缺口（本计划要补的）**：

1. 白标 / 品牌可配置层（magpie 目前品牌全部硬编码）。
2. 账号体系（magpie 是纯本地应用，没有账号登录；需要对接 sub2api 类接口）。
3. 中转站一键接入（预设远程下发 + 登录自动获取/创建 Key）。
4. 自定义更新分发（magpie 更新源硬编码为 usemagpie.ai）。

---

## 2. 架构总览

```
┌──────────────────────────────────────────────────────────────┐
│ 编译期唯一注入（ldflags）：                                     │
│   -X .../internal/brand.configURL=https://你的站/api/desktop/config.json │
└───────────────────────────┬──────────────────────────────────┘
                            │ 启动时 GET + 磁盘缓存 + 失败回退内置默认
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ 远程配置 config.json（你服务器上的唯一真源）                       │
│  brand / updates / partners / account / provider / onboarding │
└──┬──────────┬──────────────┬───────────────┬─────────────────┘
   │          │              │               │
   ▼          ▼              ▼               ▼
 品牌名/图标  更新 feed     中转站预设       账号 API
 窗口/托盘   → 你的服务器   → partners JSON  → 登录/建 Key
   │          │              │               │
   └──────────┴──────┬───────┴───────────────┘
                     ▼
       一键配置所有 Agent（复用 magpie wiring）
```

**写死的边界**：
- 允许写死的只有两样：① 远程配置 URL（编译期注入，可用 `MAGPIE_BRAND_CONFIG` 环境变量覆盖，供调试）；② 配置拉取失败时的**中性兜底值**（不出现任何具体品牌/域名）。
- 其余一切（品牌名、图标、官网、更新地址、资产前缀、中转站 base URL、线路列表、账号 API 路径、引导页文案）均在远程配置中。

---

## 3. 远程配置规范（`config.json`）

服务器上的文件（示例见 [whitelabel/config.example.json](whitelabel/config.example.json)）：

```jsonc
{
  "schema": 1,
  "brand": {
    "name": "你的桌面助手",          // 窗口标题、托盘、欢迎页
    "shortName": "你的品牌",          // 文案中品牌名替换用
    "site": "https://你的站",
    "icon": "https://你的站/icon-256.png"
  },
  "updates": {
    "feed": "https://你的站/api/desktop/latest",   // magpie 现有 feed 格式
    "assetPrefix": "yourdesk"                      // 资产前缀，见 §7
  },
  "partners": "https://你的站/api/desktop/partners",
  "account": {
    "baseURL": "https://你的站",
    "login":        "/api/v1/auth/login",
    "register":     "/api/v1/auth/register",
    "sendCode":     "/api/v1/auth/send-code",
    "listKeys":     "/api/v1/keys",
    "createKey":    "/api/v1/keys",
    "listGroups":   "/api/v1/groups",
    "oauthStart":   "/api/v1/auth/oauth/start",
    "oauthPoll":    "/api/v1/auth/oauth/poll"
  },
  "provider": {                       // 默认接入的中转站（provider 预设）
    "id": "yourrelay",
    "name": "你的中转站",
    "kind": "relay",
    "chat":        "https://api.你的站/v1",
    "responses":   "https://api.你的站/v1",
    "anthropic":   "https://api.你的站",
    "keysUrl":     "https://你的站/keys",
    "website":     "https://你的站",
    "lines": [                        // 多线路（本地路由用）
      { "name": "HK", "chat": "https://hk.你的站/v1" },
      { "name": "US", "chat": "https://us.你的站/v1" }
    ]
  },
  "onboarding": {
    "welcomeURL": "https://你的站/desktop",
    "registerURL": "https://你的站/register"
  }
}
```

约定：
- 所有 URL 都允许**相对路径**（相对 `account.baseURL` / `brand.site`）。
- 字段可增删，旧客户端忽略未知字段；`schema` 用于破坏性变更。
- 客户端磁盘缓存最近 7 天的配置；拉取失败用缓存，再失败用中性兜底。

---

## 4. 代码改造清单

### 4.1 新增（与上游零冲突，独立文件/包）

| 文件 | 职责 |
| --- | --- |
| `internal/brand/brand.go` | 配置模型、`Get()`、后台拉取、磁盘缓存、`MAGPIE_BRAND_CONFIG` 覆盖 |
| `internal/brand/brand_test.go` | 拉取/缓存/回退/字段解析测试 |
| `internal/brand/icon.go` | 图标下载到 `appdir` 缓存，供托盘/窗口使用 |
| `internal/gui/assets/brand.js` | 前端品牌覆盖层：读取 `window.go` 暴露的 brand，替换可见文案中的品牌名 |
| `whitelabel/config.example.json` | 配置样例 |
| `whitelabel/feed.json.example` | 更新 feed 样例 |
| `whitelabel/build.sh` | 三平台构建 + ldflags 注入 + feed 生成 |
| `whitelabel/PLAN.md` | 本文件 |

### 4.2 上游文件的"接缝"改动（每个 ≤10 行，`// whitelabel:` 注释标记）

| 文件 | 改动 | 侵入度 |
| --- | --- | --- |
| [internal/update/update.go](internal/update/update.go#L42-L52) | `const Site` → `func Site()`；`Feed()` 优先读 `brand.Get().Updates.Feed` | 小 |
| [internal/update/update.go](internal/update/update.go#L224-L226) | `AppAsset()`/`BinaryAsset()` 的 `magpie-` 前缀 → `brand.AssetPrefix()` | 小 |
| [internal/provider/partners.go](internal/provider/partners.go#L35) | `const PartnersURL` → `func PartnersURL()` 读 brand | 小 |
| [internal/gui/app.go](internal/gui/app.go#L413) | `Name: "magpie"` → `Name: brand.Name()` | 小 |
| [internal/gui/app.go](internal/gui/app.go#L799) | `Title: "magpie"` → `Title: brand.WindowTitle()` | 小 |
| [internal/gui/desktop.go](internal/gui/desktop.go#L10) | `programName` → 读 brand（数据目录名保持不变，避免升级迁移） | 小 |
| `internal/gui/assets/app.js` | 末尾插入 `<script src="brand.js">` 等价的一行引入（或 i18n 初始化处调用 `brandOverlay()`） | 1 行 |
| `internal/provider/presets.go` | **不改**：中转站走 partners 远程下发，与上游机制完全一致 | 零 |

### 4.3 新增业务能力（新包，不改上游行为）

| 包 | 职责 |
| --- | --- |
| `internal/account/` | sub2api 类账号 API 客户端：登录、注册、验证码、OAuth、Key 列表/创建；路径全部来自配置 |
| `internal/onboard/` | 首次启动引导状态机：登录 → 取 Key → 选工具 → 一键配置；状态存 `appdir` |
| `internal/gui/` 新增 Wails 绑定 | `App.Login` `App.ListKeys` `App.CreateKey` `App.ConfigureAll` `App.GetOnboarding` 等；仅在 `brand.Get().Account != nil` 时注册 |

> 说明：这些包**只被新代码调用**，上游文件只需在 GUI 绑定的注册点插入一小段（`// whitelabel:`），避免散布改动。

---

## 5. 账号登录与一键接入流程

```
首次启动
  └─ 引导页（来自 onboarding 配置）
       ├─ 打开品牌官网/注册页（brand.site）
       ├─ 客户端内登录（account.* 路径，sub2api 标准响应）
       │     ├─ 成功 → 本地保存 token（appdir 下的独立文件）
       │     ├─ 列 Key → 有则选，无则自动创建（createKey）
       │     └─ 或浏览器 OAuth（oauthStart/oauthPoll，视你的站是否支持）
       ├─ 检测已装 Agent（复用 magpie agent 探测）
       ├─ 勾选工具 → ConfigureAll
       │     └─ 逐工具写带标记区块的配置，写前自动备份（magpie 现成）
       └─ 完成，托盘常驻
```

要点：
- **Token 本地存储**：单独文件（`appdir/brand/account.json`，0600），不进 settings.json，不参与备份同步。
- **Key 自动创建**：调用 `createKey`，名称取 `brand.shortName + 系统名`，避免用户看到一串随机名。
- **失败可恢复**：任何一步失败都能回退到"粘贴 Key"的经典流程（magpie 原有的添加 provider 路径不受影响）。
- **不破坏 magpie 原有流程**：未配置账号 API 时，引导页只显示"粘贴 Key"入口，行为与上游一致。

---

## 6. 品牌 UI 改造

| 位置 | 改造 |
| --- | --- |
| 窗口标题 / 托盘菜单 | Go 侧读 `brand.Name()`（§4.2 接缝） |
| 托盘图标 | `brand.icon` 下载到 appdir 缓存；失败回退上游图标 |
| 前端可见文案 | `brand.js` 覆盖层：初始化时拉取 brand，将 i18n 词条中作为专有名词的 "magpie" 替换为 `brand.shortName`；不碰 `magpie://` 协议、包名、URL |
| 欢迎/引导页 | 新页面（`onboarding` 渲染），未配置时不显示 |
| 关于页/更新说明 | 读 brand.site 与 feed |

> 取舍：不逐行改 24k 行的 `app.js`，而是"初始化一次覆盖 + 关键页面新建"。品牌覆盖面按阶段扩大，避免与上游产生大面积 diff。

---

## 7. 更新分发（走你自己的服务器）

### 7.1 服务器文件布局

```
https://你的站/
  api/desktop/config.json     → 品牌/业务配置（§3）          ← whitelabel/config.lyy.team.json
  api/desktop/latest          → feed JSON                    ← build.sh feed 生成
  api/desktop/partners        → 中转站 partner 列表（可选）   ← whitelabel/partners.lyy.team.json
  downloads/lyy-desktop-darwin-arm64.zip
  downloads/lyy-desktop-windows-amd64.zip
  downloads/lyy-desktop-linux-amd64.zip
```

`partners` 与 `config.provider` 的分工：`provider` 决定首次引导一键接入的中转站；
`partners` 让它在「添加 provider」的预设列表里排在赞助商分组（可同时存在，也可省略
`partners`，客户端会回退上游行为）。

### 7.2 feed 格式（沿用 magpie 现有格式）

```json
{
  "version": "1.6.1",
  "notes": "## 更新内容\n- …",
  "url": "https://你的站/desktop",
  "assets": {
    "yourdesk-darwin-arm64.zip":   { "url": "https://…", "size": 12345678, "sha256": "…" },
    "yourdesk-windows-amd64.zip":  { "url": "https://…", "size": 12345678, "sha256": "…" },
    "yourdesk-linux-amd64.zip":    { "url": "https://…", "size": 12345678, "sha256": "…" }
  }
}
```

- 资产名（与 `update.AppAsset` / `update.BinaryAsset` 完全一致）：
  - macOS：`{prefix}-darwin-{arch}.zip`（整个 app 包）
  - Windows：`{prefix}-windows-{arch}.exe`（裸 exe）
  - Linux：`{prefix}-linux-{arch}`（裸二进制，无扩展名）

  自动更新是下载后替换自身，**feed 里放 zip 会让更新失败**；官网下载页用的
  zip 由构建脚本另出一份，只用于手动下载。
- SHA-256 必填，客户端校验后才安装（沿用上游逻辑）。
- 版本号保持语义化 `x.y.z`；首版建议从 `1.6.1` 起（对齐参考产品），之后单调递增。

### 7.3 发布流水线（`whitelabel/build.sh`）

1. `go build -ldflags "-X .../internal/brand.configURL=<你的配置地址> -X main.version=<版本>"` × 3 平台 × 2 架构。
2. 打包 zip（macOS 用 app bundle，沿用上游 `build/` 逻辑）。
3. 计算 SHA-256，生成 `feed.json`。
4. 上传到服务器（rsync / 对象存储，脚本预留变量）。

---

## 8. 上游合并策略（关键）

### 8.1 分支模型

```
origin/main   ← 上游 yetone/magpie（只拉取）
my/main       ← 你的仓库 main，与上游同步，零二开改动
my/dev        ← 二开分支，所有改动在这里
my/release    ←（可选）发布分支，由 dev 合并，打 tag
```

- 上游更新：`git switch main && git pull origin main && git push my main`（保持 my/main 与上游一致，作为合并基线）。
- 同步到二开：`git switch dev && git merge my/main`（或 rebase，见 8.3）。

### 8.2 冲突控制三原则

1. **新增优先**：能新建文件/包就不改上游文件（§4.1）。
2. **接缝最小化**：必须改上游文件时，只加"读配置"的调用，不改行为；每处改动前后加 `// whitelabel: <原因>` 注释，合并时一眼可辨。
3. **业务隔离**：所有品牌/账号/引导逻辑在 `internal/brand`、`internal/account`、`internal/onboard` 与 `internal/gui` 的新绑定里；上游包不 import 业务包（方向单向：业务 → 上游，不反向）。

### 8.3 合并节奏与验证

- 每周一次 `git fetch origin && git merge origin/main` 进 dev；冲突文件预期 ≤ 5 个（§4.2 的接缝文件）。
- 每次合并后跑：`go build ./... && go test ./internal/brand ./internal/account ./internal/onboard ./internal/update ./internal/provider ./internal/gui`。
- 上游若大改接缝处（如 update 包重构），优先改用"运行时覆盖"方案（`MAGPIE_BRAND_CONFIG`），减少接缝。

### 8.4 不做的改动

- 不改上游包名、模块路径（`github.com/yetone/magpie` 保持），避免全仓库 diff；品牌只在**显示层与配置层**体现。
- 不改数据目录名（`appdir`），旧用户升级不丢配置；品牌只影响显示与分发。

---

## 9. 阶段计划

### 阶段 0 · 仓库与分支（已完成）
- `my` remote 指向你的仓库；`dev` 分支已建。

### 阶段 1 · 白标底座（已完成）
- [x] `internal/brand` 包（拉取/缓存/回退/环境变量覆盖/文件路径调试）
- [x] update 包接缝：`Site()`/`Feed()`/资产前缀
- [x] partners 接缝：`PartnersURL()`
- [x] GUI 窗口/托盘接缝
- [x] `whitelabel/config.example.json` + `feed.json.example`
- [x] 验收通过：本地 config 服务 → 更新检查走自定义 feed → `magpie presets` 出现远程 partners 条目

### 阶段 2 · 品牌 UI（已完成）
- [x] 前端文案覆盖（`i18n.js` 的 `t()` 里替换品牌名，排除 `magpie://` 等标识）
- [x] `bootPrefs.brand` 注入（形状：name/shortName/site）
- [x] 引导页骨架（`onboard.js`，无账号 API 的构建自动不显示）
- [x] 托盘/窗口图标从配置下载（`internal/brand/icon.go`：PNG 校验、缓存、记录来源
      URL；macOS/Linux 取剪影，Windows 用彩色；失败回退内置图标）
- [x] 验收通过：品牌名在窗口、托盘 tooltip 与页面文案一致；标识不受影响

### 阶段 3 · 账号与一键接入（已完成，待真实环境验收）
- [x] `internal/account`（sub2api 兼容客户端，路由与字段名全配置化）
- [x] `internal/onboard`（登录 → 取/建 Key → 添加中转站 → 拉模型 → 连接工具 → 完成）
- [x] GUI 路由 8 个（`internal/gui/onboard.go`）+ CLI `magpie onboard …`
- [x] 验收通过：本地模拟中转站，登录 → 自动建 Key → `acme-relay` 就绪（ready: true）→ finish

### 阶段 4 · 功能对齐（大部分可复用上游）
- [x] 多线路自动切换：配置的 `provider.lines` 自动生成每线路一个 provider，
      再加一个路由组（每模型的各线路相邻），网关按顺序 failover —— 即
      「线路出问题，自动换一条」
- [x] 线路状态：`magpie onboard lines check` 与 `GET /api/onboard/lines?probe=1`
      并发探测每条线路的延迟，引导页在接入后展示
- [x] MCP / 提示词 / Skills / 会话页面：上游已有，品牌文案由 `t()` 覆盖层自动替换
- [x] 工具检测：上游已有（`agent.Detected`），引导页按检测结果显示
- [x] 线路切换：选不同线路的 provider 即可（上游 Providers/Agents 页面）
- [ ] 工具一键安装：上游的既有设计是「展示安装命令」，不代执行（全局 npm
      前缀可能需 sudo、新机器缺 Node.js）；如要自动执行需另做，风险较高

### 阶段 5 · 发布流水线（脚本已完成，待真实域名）
- [x] `whitelabel/build.sh` 三平台构建 + zip + feed 生成
- [ ] 首版发布到你的服务器，验证客户端自动更新

---

## 10. 风险与注意事项

| 风险 | 应对 |
| --- | --- |
| 上游 update 包重构导致接缝冲突 | 接缝集中且带标记；极端情况改用环境变量覆盖 |
| 品牌替换遗漏（前端 24k 行） | 覆盖层优先覆盖高频页面；逐阶段扩大；不做全量替换 |
| 配置服务器不可用 | 磁盘缓存 7 天 + 中性兜底；不阻断核心功能 |
| 账号 API 与 sub2api 版本差异 | 路径全配置化；登录响应做兼容解析（token 字段名可配置） |
| 许可证与品牌 | magpie 为 MIT：允许二开与再分发，保留版权声明；分发版不要使用上游商标/图标 |
| 冷启动无网络 | 引导页提供"粘贴 Key"离线路径 |

---

## 11. 需要你提供的信息

1. 你的中转站域名（`brand.site` 与 `api` 域名）。
2. 多线路列表（如 HK/US/…，以及每条的 base URL）。
3. 账号 API 的实际路径（sub2api 默认 `/api/v1/auth/login`、`/api/v1/keys`；有差异就填配置）。
4. 品牌名与图标文件。
5. 更新 feed 与安装包的托管方式（自有服务器静态目录即可）。
