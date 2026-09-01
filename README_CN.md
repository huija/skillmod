# skillmod —— go mod for Agent Skills

[English](README.md) | 简体中文

面向 agent 项目的 skill 依赖管理器，设计血统来自 go mod：
**项目内声明依赖（SKILL.mod）+ 内容锁定（SKILL.lock）+ 一键对齐（sync）**。

与 AGENTS.md 的关系：AGENTS.md 告诉 agent *怎么行为*，SKILL.mod 声明 agent *需要什么能力*。

## 这是什么问题

skill（指令 + 脚本的打包单元）决定 agent 行为，但当前管理停留在"前依赖管理时代"：手工复制、git submodule、各平台市场。后果：同团队不同机器 agent 行为不一致；出问题回答不了"当时用的是哪个版本"；内容被篡改无法察觉。

skillmod 用 go mod 的同构方案解决：`SKILL.mod` 声明 + `SKILL.lock` 锁定（dirhash 内容寻址）+ `skillmod sync` 幂等对齐，任何机器得到完全一致的 skill 集合。

## v0.1 范围

- **CLI 七个命令**：`init` / `get` / `sync` / `list` / `update` / `prune` / `verify`
- **源直接走 git**（对应 go 的 direct 模式）：skill = 带 tag 的 repo 或 monorepo 子目录（`<repo>//<subdir>`），打 tag 即发布；无服务端、无 registry
- **版本三种形态**：semver tag / commit SHA / 伪版本（无 tag 仓兜底）；分支名拒绝——可变引用不可锁定
- **共享持久存储**：`~/.agents/skillmod/pkg/mod/<域名>/<组织>/<repo>@<版本>` 保存可读、只读的整仓版本快照；bare Git、refs 与解析元数据位于 `pkg/mod/cache`，HTTPS / 默认端口 SSH / `.git` 变体共享；可用 `SKILLMOD_HOME` 覆盖
- **拷贝安装**：字节级拷贝（Windows 兼容），sync 永不自动删除文件，清理走确认制 `prune`
- **扁平 1:1**：无传递依赖解析（`requires` 字段预留），无约束求解器
- **零遥测**

## 示例

```toml
# SKILL.mod（人维护，入库）
schemaversion = 1

[[skill]]
name = "code-review"
source = "github.com/acme/agent-skills//code-review"
version = "code-review/v1.2.0"

[[skill]]
name = "legacy-notes"
local = true
```

```toml
# SKILL.lock（工具维护，禁止手改；纯函数，无时间戳）
[[skill]]
name = "code-review"
source = "github.com/acme/agent-skills//code-review"
version = "code-review/v1.2.0"
commit = "7f3a9c1e00000000000000000000000000000000"
dirhash = "h1:4wYq0b..."
```

```bash
skillmod init                                          # 扫描现有 skill 生成声明
skillmod get github.com/anthropics/skills//skills/pdf  # 添加（无 tag 仓自动落伪版本）
skillmod sync                                          # 按 lock 对齐，幂等
skillmod verify                                        # CI 校验，漂移退出码非零
```

### 命令输出语言

命令帮助、执行摘要、交互提示、错误信息及 JSON 中的人类可读说明优先采用显式设置的 `SKILLMOD_LANG`。未设置时，skillmod 按 `LC_ALL` → `LC_MESSAGES` → `LANG` 读取第一个非空的系统 locale。目前识别英文和中文 locale；未设置或无法识别时回退到英文。可用 `SKILLMOD_LANG=zh` 显式选择中文；也支持 `en_US.UTF-8`、`zh_CN.UTF-8` 这类 locale 值。

```bash
SKILLMOD_LANG=zh skillmod sync
```

JSON 的字段名和供程序消费的 action 标识不会翻译。

翻译统一维护在 [`locales/`](locales/) 下对等的 gettext/POSIX locale catalog：`en_US.po` 和 `zh_CN.po` 拥有完全相同的 msgid 集合。CLI 构建时会同时嵌入两份文件，后续官网和文档工具也能通过统一的 `{{locale}}.po` 路径选择语言；`SKILLMOD_LANG=en` 与 `SKILLMOD_LANG=zh` 仍作为便捷别名。修改用户可见文案后运行：

```bash
go generate ./internal/i18n
```

第一次获取某个 `repo@version` 时会物化完整仓库版本；之后添加该版本下的其他 skill，直接从本地子目录校验并安装，不调用 Git、不访问远端。显式 `@commit` 同样可通过已有 repo commit 快照复用。省略版本的 latest 与 `skillmod update` 保留联网刷新语义。

```text
~/.agents/skillmod/pkg/mod/
├── github.com/anthropics/skills@v0.0.0-.../  # 可直接浏览的整仓快照
└── cache/
    ├── vcs/                                  # bare Git（内部哈希 key）
    ├── download/                             # repo 版本/refs/解析元数据
    └── locks/
```

Store v2 不自动迁移或删除旧的根级 `cache/` 与哈希命名子树快照；首次使用新版本时会按新布局重新物化，旧目录可在确认不再回滚旧二进制后手工清理。

默认安装到项目的 `.agents/skills/`。Claude Code 需要额外落盘时，在
`~/.config/skillmod/config.toml` 中配置：

```toml
agents = ["agents", "claude-code"]
```

安装目录是由 lock 重建的产物，建议项目 `.gitignore` 忽略 `.agents/skills/`
（启用 Claude 适配器时也忽略 `.claude/skills/`），只提交 `SKILL.mod` 与 `SKILL.lock`。

## 非目标（v0.1）

- 无站点、无 registry 服务（留给未来的 skill-hub 项目）
- 无传递依赖、无版本约束求解
- 无遥测、无 skill 内容安全扫描（另属后续产品线）

## 文档

| 文档 | 内容 |
|---|---|
| [docs/prd-v0.1.md](docs/prd-v0.1.md) | 产品规格（已定稿）：命令、字段、异常、验收标准 |
| [docs/dev-design.md](docs/dev-design.md) | 研发方案：选型、模块、流程、测试、里程碑 |
| [docs/design.md](docs/design.md) | 流转图、实现注记、设计决策史 |
| [docs/prd-feedback.md](docs/prd-feedback.md) | 三轮评审往返存档 |

## 后续路线（不在 v0.1）

- v0.2 候选：meta-skill（agent 经确认门自助拉取）、SessionStart hook 自动 sync
- 供应链安全扫描、企业策略门禁、中心注册中心（生态成熟后再议）

> 供应链安全原则（meta-skill 时代适用）：**agent 可发起，变更必过人**。
> 硬门 = 宿主 Bash 权限确认；软门 = meta-skill 红线。安全靠确认门而非藏工具。

## 许可证

skillmod 基于 [MIT License](LICENSE) 发布。
