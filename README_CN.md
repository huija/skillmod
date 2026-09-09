# skillmod —— go mod for Agent Skills

[English](README.md) | 简体中文

面向 agent 项目的 skill 依赖管理器，设计血统来自 go mod：
**项目内声明依赖（SKILL.mod）+ 内容锁定（SKILL.lock）+ 一键对齐（sync）**。

与 AGENTS.md 的关系：AGENTS.md 告诉 agent *怎么行为*，SKILL.mod 声明 agent *需要什么能力*。

## 这是什么问题

skill（指令 + 脚本的打包单元）决定 agent 行为，但当前管理停留在"前依赖管理时代"：手工复制、git submodule、各平台市场。后果：同团队不同机器 agent 行为不一致；出问题回答不了"当时用的是哪个版本"；内容被篡改无法察觉。

skillmod 用 go mod 的同构方案解决：`SKILL.mod` 声明 + `SKILL.lock` 锁定（dirhash 内容寻址）+ `skillmod sync` 幂等对齐，任何机器得到完全一致的 skill 集合。

## 安装

使用 Go 1.26.1 或更高版本：

```bash
go install github.com/huija/skillmod@latest
```

从仓库源码进行本地开发时：

```bash
make install
```

该命令会把 `skillmod` 安装到 `go env GOBIN`；若未设置 `GOBIN`，则安装到第一个 `GOPATH/bin`（Windows 下为 `%GOPATH%\bin`），并将当前 Git revision 写入开发版本号。Windows 下请在 Git Bash（提供 `sh`）中运行 Makefile，或用 `make install INSTALL_DIR=/usr/local/bin` 覆盖安装目录；所选目录需位于 `PATH` 中。

不使用 Go 时，从 [GitHub Releases](https://github.com/huija/skillmod/releases) 下载对应平台的压缩包，解压后将 `skillmod` 放入 `PATH`。

## 前置条件

skillmod 通过系统 `git` 可执行文件获取源码，因此必须安装 Git 并加入 `PATH`（Windows 请安装 [Git for Windows](https://gitforwindows.org/)，系统默认不带）；SSH 远程地址还需要 `PATH` 中有 `ssh`。

## 功能

- **CLI 九个命令**：`init` / `get` / `sync` / `list` / `why` / `update` / `remove` / `prune` / `verify`
- **源直接走 git**（对应 go 的 direct 模式）：skill = 带 tag 的 repo 或 monorepo 子目录（`<repo>//<subdir>`），打 tag 即发布；无服务端、无 registry
- **版本三种形态**：semver tag / commit SHA / 伪版本（无 tag 仓兜底）；分支名拒绝——可变引用不可锁定
- **共享持久存储**：`~/.agents/skillmod/pkg/mod/<域名>/<组织>/<repo>@<版本>` 保存可读、只读的整仓版本快照；bare Git、refs 与解析元数据位于 `pkg/mod/cache`，HTTPS / 默认端口 SSH / `.git` 变体共享；可用 `SKILLMOD_HOME` 覆盖
- **优先链接安装**：默认 `auto` 链接到共享只读快照；系统不支持软链接时自动拷贝，Windows 无链接权限也可使用。需要独立目录时使用 `copy`
- **接管已有技能**：`init` 扫描普通目录与目录链接；`--global` 管理用户级技能，项目和全局复用同一份缓存
- **扁平 1:1**：无传递依赖解析，无约束求解器
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
skillmod get github.com/openai/skills//gh-fix-ci       # 唯一 skill 名可缩写嵌套路径
skillmod sync                                          # 按 lock 对齐，幂等
skillmod why pdf                                       # 说明来源和各目标状态
skillmod remove pdf                                    # 删除声明和内容未改动的受管安装
skillmod verify                                        # CI 校验，漂移退出码非零
```

`//` 后只有一段时，先按仓库根目录的精确子目录解析，不存在时再按 `skills/` 下的唯一 skill 名匹配；同名时必须使用完整路径。省略 `//<子目录>` 时，`get` 会同时发现仓库根目录的 `SKILL.md` 和 `skills/` 下全部层级的 `SKILL.md`。交互式终端会以紧凑的彩色单行列表展示候选；展示命令会省略冗余的 `https://` 前缀，并在安全时优先使用唯一 skill 名简写。可用 ↑/← 移到上一项、↓/→ 移到下一项，空格勾选或取消，D 键展开或收起当前项的描述与命令，回车确认。`--yes` 会安装发现的全部 skill。

不同来源发布同名 skill 时，可用 `--alias <目录名>` 安装新增条目；两份声明和锁记录都会保留。安装目录与 `name` 相同时，锁记录省略 `dir`，仅 alias 条目记录该字段。alias 必须是跨平台合法名称，所有安装目录经过 Unicode 规范化和大小写折叠后仍须唯一，确保同一项目在 Linux、macOS 和 Windows 上行为一致。对同一 source 改用另一 alias 重新 get 时，旧目录会保留，并提示可用 `skillmod prune` 清理。`skillmod update <name>` 会更新所有发布名称相同的条目；传入 alias 可只更新对应安装。

耗时较长的 `get` 和 `update` 会在交互式终端显示紧凑的动态状态区：主阶段显示在 spinner 旁，同时存在的子状态以弱色拼接在第二行。远程版本检查只请求 HEAD、分支和 tag refs；`update` 会合并同一仓库的等价 URL，并最多并发检查四个不同仓库。同一命令内，仓库缓存快照只做一次完整性校验，skill 名发现和批量选择会复用该结果。

### 接管已有目录：项目与全局

```bash
skillmod init --yes --dry-run          # 预览当前项目的导入结果
skillmod init --yes                    # 登记当前项目已有技能
skillmod --global init --yes           # 登记用户已有技能
skillmod --global list
skillmod --global verify
```

`init` 扫描所选范围的 `.agents/skills/` 和 `.claude/skills/`，保留原有目录、链接和文件。有效目录链接会跟随到内容进行校验；失效链接、不可校验内容和非法目录名会列出并跳过。同名目录在两个平台中内容不一致时，需要先整理或重命名；内容相同时会合并条目，但保留所有候选路径用于恢复来源。

来源恢复优先使用匹配的旧锁记录或经过校验的 skillmod 缓存快照（包括 monorepo 子目录与 alias）。`init --global` 还会导入上游安装器的 `.skill-lock.json`，项目初始化会读取 `skills-lock.json`。旧记录包含 Git revision 时以该不可变版本为准；缺少 revision 时，仅当远端最新不可变解析与已安装内容完全一致才接管为远程技能。无法安全解析的来源会保留为本地基线，单个条目失败不会丢弃本次导入的其他结果。导入同时生成 `SKILL.mod` 和 `SKILL.lock`；已有声明需要 `--force`，写入前会备份为 `SKILL.mod.bak`。

| 范围 | 声明与锁文件位置 | 默认技能安装位置 |
| --- | --- | --- |
| 项目（默认） | 当前目录的 `SKILL.mod`、`SKILL.lock` | 当前目录的 `.agents/skills/` |
| 全局（`--global`） | `$SKILLMOD_HOME/global/`，默认 `~/.agents/skillmod/global/` | `~/.agents/skills/` |

**缓存只有一份**：两个范围都使用 `$SKILLMOD_HOME/pkg/mod/`（默认 `~/.agents/skillmod/pkg/mod/`）。`global/` 仅存清单，不包含另一份快照缓存。启用 Claude Code 时，全局安装目录为 `~/.claude/skills/`。所有九个命令都支持 `--global`；默认命令不会自动合并项目与全局清单。`--dry-run` 不写清单或安装目录，远程来源校验可能填充共享缓存。

### 安装方式与旧拷贝迁移

```bash
skillmod sync --relink --dry-run       # 预览已锁定远程技能的重新安装
skillmod sync --relink                 # 把内容一致的旧拷贝转为链接，不能链接则拷贝
skillmod --global sync --relink        # 同样适用于全局
skillmod sync --relink --install-mode=copy  # 转成独立、可编辑的目录
```

普通 `sync` 保留内容已一致的安装，维持幂等；`--relink` 显式按所选安装方式重装远程条目。两者都遵守本地修改的冲突处理规则，`--yes` 不会强制覆盖冲突；本地条目只登记和校验，不会自动迁移。

`get`、`sync`、`update`、`remove` 在独立操作已完成、但有目标被安全保留时返回退出码 3。JSON 报告提供 `targetResults`，自动化可以区分 `install`、`installed`、`keep`、`skip`、`missing`、`drift` 等动作。远端较新 tag 消失时，`update` 默认拒绝静默降级；明确需要降级时使用 `--allow-downgrade`。

`skillmod remove <名称或别名>` 会在一个可回滚事务中删除匹配的声明和内容未改动的受管安装；本地修改过或无法验证的目录会保留，并报告为部分完成。`skillmod why <名称或别名>` 展示来源、解析版本、commit、dirhash、alias 目录和每个安装目标的状态。

`auto` 优先使用系统目录软链接，创建失败时回退为字节级拷贝；`copy` 始终创建独立目录。可通过 `--install-mode` 临时覆盖配置中的 `install_mode`。链接指向共享只读快照，需要编辑时先使用 `copy` 模式分离目录。技能内部的符号链接仍不支持。

安装方式、缓存绝对路径和作用范围均不写入 `SKILL.mod` / `SKILL.lock`，因此相同声明与版本在不同系统、不同安装模式下仍产生相同的清单与锁文件。更新只切换当前范围的安装入口；`prune` 删除被移除条目的安装入口，不删除链接目标或共享缓存。缓存目前不会自动回收；手动清理快照前须确认没有安装链接使用它。

### 命令输出语言

命令帮助、执行摘要、交互提示、错误信息及 JSON 中的人类可读说明优先采用显式设置的 `SKILLMOD_LANG`。未设置时，skillmod 按 `LC_ALL` → `LC_MESSAGES` → `LANG` 读取第一个非空的系统 locale。目前识别英文和中文 locale；未设置或无法识别时回退到英文。可用 `SKILLMOD_LANG=zh` 显式选择中文；也支持 `en_US.UTF-8`、`zh_CN.UTF-8` 这类 locale 值。

```bash
SKILLMOD_LANG=zh skillmod sync
```

JSON 的字段名和供程序消费的 action 标识不会翻译。

翻译统一维护在 [`locales/`](locales/) 下对等的 gettext/POSIX locale catalog：`en_US.po` 和 `zh_CN.po` 拥有完全相同的 msgid 集合。CLI 构建时会同时嵌入两份文件；`SKILLMOD_LANG=en` 与 `SKILLMOD_LANG=zh` 仍作为便捷别名。修改用户可见文案后运行：

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

默认安装到项目的 `.agents/skills/`。Claude Code 需要额外落盘时，在配置文件中设置 `agents`。配置文件位置遵循 `os.UserConfigDir()`：

| 操作系统 | 配置文件路径                                         |
| -------- | ---------------------------------------------------- |
| Linux    | `~/.config/skillmod/config.toml`                     |
| macOS    | `~/Library/Application Support/skillmod/config.toml` |
| Windows  | `%AppData%\skillmod\config.toml`                     |

```toml
agents = ["agents", "claude-code"]
install_mode = "auto" # auto / copy
```

安装目录是由 lock 重建的产物，建议项目 `.gitignore` 忽略 `.agents/skills/`
（启用 Claude 适配器时也忽略 `.claude/skills/`），只提交 `SKILL.mod` 与 `SKILL.lock`。

## 当前限制

- 无 registry 服务
- 无传递依赖、无版本约束求解
- 无遥测、无 skill 内容安全扫描

## 许可证

skillmod 基于 [MIT License](LICENSE) 发布。
