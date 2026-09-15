# skillmod —— go mod for Agent Skills

[English](README.md) | 简体中文

面向 agent 项目的 skill 依赖管理器，设计血统来自 go mod：
**在 `SKILL.mod` 里声明项目需要什么，用 `SKILL.lock` 锁定内容，再由 `skillmod sync` 让每台机器完全一致。**

与 AGENTS.md 的关系：AGENTS.md 告诉 agent *怎么行为*，`SKILL.mod` 声明 agent *需要什么能力*。

## 为什么需要它

skill（指令 + 脚本的打包单元）决定 agent 的行为，但它的管理停留在前依赖管理时代。手工拷贝留不下"装了什么"的记录；git submodule 让每个使用方都要克隆整仓、用 git 才能拿到一个 Markdown 目录；各平台市场把技能装进机器本地状态，于是同事的 agent、CI 和你自己的笔记本会悄悄分叉。症状是相同的：同团队不同机器行为不一致、"当时用的是哪个版本"回答不了、内容被改过或篡改也无法察觉。

skillmod 用 go mod 的同构方案解决：`SKILL.mod` 声明 + `SKILL.lock`（dirhash 内容寻址）锁定 + `skillmod sync` 幂等对齐。没有需要运维的 registry——发布一个 skill 就是在自己的仓库里打个 tag。单机单技能并不需要它；当第二台机器、同事或 CI 要共用同一套技能时，这份声明就开始回本。

## 你能得到什么

- **每台机器拿到逐字节相同的技能。** `sync` 比对的是内容而非版本号，且幂等，重复执行不会有变化。
- **"当时用的是哪个版本"有答案。** 锁文件记录请求的版本、解析出的 commit 和内容哈希，tag、裸 commit、无 tag 仓库的伪版本都适用。
- **改动与篡改会被发现。** `verify` 把已安装内容与锁文件比对，漂移时以非零码退出，因此可以直接当作 CI 关卡。
- **本地修改不会被静默覆盖。** 被改过的安装会保留下来并以退出码 3 报告，把决定权交回给人。
- **技能只取一次，之后复用。** 同一仓库版本的不可变快照在整机共享，安装用链接而不是拷贝，所以第二个项目直接从磁盘安装，不可变版本离线也能装。
- **不需要额外基础设施。** 无 registry、无服务端、无遥测，外部依赖只有 Git。

## 五分钟

```console
$ skillmod get github.com/openai/skills//skills/.curated/gh-fix-ci --install-mode=copy --yes
已安装 gh-fix-ci v0.0.0-20260624023612-49f948faa925，SKILL.mod 与 SKILL.lock 已更新
```

`SKILL.mod` 是人维护、需要提交的声明；`SKILL.lock` 由 skillmod 写入，记录声明解析出的结果：

```toml
# SKILL.mod —— 人维护
schemaversion = 1

[[skill]]
name = 'gh-fix-ci'
source = 'github.com/openai/skills//skills/.curated/gh-fix-ci'
version = 'v0.0.0-20260624023612-49f948faa925'
```

```toml
# SKILL.lock —— skillmod 维护
schemaversion = 1

[[skill]]
name = 'gh-fix-ci'
source = 'github.com/openai/skills//skills/.curated/gh-fix-ci'
version = 'v0.0.0-20260624023612-49f948faa925'
commit = '49f948faa9258a0c61caceaf225e179651397431'
dirhash = 'h1:kiGlVBeTCF8Q9f0rPATnDn1jBn/obT3TTTL8D8gdCbM='
```

现在像"一次没人审查的修改"那样改动已安装的技能，再问项目是否仍与锁文件一致：

```console
$ echo "先 rebase。" >> .agents/skills/gh-fix-ci/SKILL.md

$ skillmod verify
校验结论：有漂移
$ echo $?
2
```

`why` 解释这个条目的来源，以及它哪里不对：

```console
$ skillmod why gh-fix-ci
gh-fix-ci（目录 gh-fix-ci）：github.com/openai/skills//skills/.curated/gh-fix-ci v0.0.0-20260624023612-49f948faa925
  提交：49f948faa9258a0c61caceaf225e179651397431
  目录哈希：h1:kiGlVBeTCF8Q9f0rPATnDn1jBn/obT3TTTL8D8gdCbM=
  /tmp/agent-project/.agents/skills/gh-fix-ci：drift
```

`sync` 不会为了让报告好看而丢掉这次修改：

```console
$ skillmod sync --yes
冲突（--yes 自动保留并跳过）: /tmp/agent-project/.agents/skills/gh-fix-ci
没有变更；已保留 1 个冲突目标
$ echo $?
3
```

退出码 3 正是关键：能独立完成的工作已经完成，而关于这次修改的决定留给你。你可以把它当作项目的本地版本保留，也可以交互式运行 `skillmod sync` 并选择 *overwrite* 恢复锁定内容。

## 安装

### 使用 Agent 引导安装（推荐）

准备好 Node.js/npm 后，全局安装仓库内附带的 Agent Skill，让编码 Agent 可以在任意项目中安装和使用 skillmod：

```bash
npx skills add huija/skillmod --skill skillmod --global
```

然后告诉 Agent：

```text
安装 skillmod，并确保它可以从 PATH 调用。
```

该 Skill 会检查 Git 前置条件和已有的 skillmod 安装，识别当前操作系统与架构，选择对应的 Release Binary，使用发布的 SHA-256 校验和进行验证，将可执行文件安装到 `PATH` 中用户可写的目录，并验证安装结果。如果只希望当前项目使用这份引导，请省略 `--global`。

### 手动安装

不使用 Go 时，从 [GitHub Releases](https://github.com/huija/skillmod/releases) 下载对应平台的压缩包和 `checksums.txt`，校验压缩包后解压，并将 `skillmod` 放入 `PATH`。

使用 Go 1.26.1 或更高版本：

```bash
go install github.com/huija/skillmod@latest
```

从仓库源码本地开发时，`make install` 会把二进制装到 `go env GOBIN`；未设置 `GOBIN` 时用第一个 `GOPATH/bin`，并写入当前 Git revision。详见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 前置条件

skillmod 通过系统 `git` 可执行文件获取源码，因此必须安装 Git 并加入 `PATH`（Windows 请安装 [Git for Windows](https://gitforwindows.org/)，系统默认不带）；SSH 远程地址还需要 `PATH` 中有 `ssh`。

## 常用命令

| 命令 | 作用 |
| --- | --- |
| `init` | 把磁盘上已有的技能登记进 `SKILL.mod` 和 `SKILL.lock` |
| `get <地址>` | 添加并安装一个技能 |
| `sync` | 按锁文件对齐安装，幂等，且不会覆盖本地修改 |
| `list` | 列出全部声明、版本和安装状态 |
| `why <选择器>` | 说明单个条目：来源、解析版本、commit、dirhash 和各目标状态 |
| `update [选择器]` | 把条目更新到最新的不可变版本；拒绝静默降级 |
| `verify` | 校验已安装内容与锁文件是否一致，即 CI 关卡 |
| `remove <选择器>` | 删除声明和内容未改动的受管安装 |
| `prune` | 清理手工编辑后残留的过期安装和锁记录 |

所有命令都支持 `--json`（机器可读输出）和 `--global`（作用于用户级技能而非当前项目），写操作支持 `--dry-run`。命令帮助、摘要、交互提示和错误信息优先采用 `SKILLMOD_LANG`，未设置时跟随系统 locale；JSON 的字段名和 action 标识不会翻译。

## 详细文档在哪

这份 README 不再扩写成手册：具体规则放在操作 skillmod 的那个 Agent Skill 里，人和 agent 都可以读。**这些参考文档目前只有英文版。**

| 阅读 | 内容 |
| --- | --- |
| [manifests.md](skills/skillmod/references/manifests.md) | 清单规则、地址与版本形态、alias，以及清单刻意不记录的内容 |
| [storage.md](skills/skillmod/references/storage.md) | 缓存布局、安装方式、配置文件、声明与安装的实际位置 |
| [automation.md](skills/skillmod/references/automation.md) | 退出码，以及 CI 需要分支处理的 JSON 报告词表 |
| [setup.md](skills/skillmod/references/setup.md) | 安装或升级可执行文件、PATH 诊断 |
| [use-cases.md](skills/skillmod/references/use-cases.md) | 分场景操作与故障排查 |
| [issue-reporting.md](skills/skillmod/references/issue-reporting.md) | 准备经过脱敏的 Bug 报告 |
| [CONTRIBUTING.md](CONTRIBUTING.md) | 构建、测试和改 skillmod 本身 |

## 当前限制

- 无 registry 服务：一个 skill 就是一个 Git 仓库，发布即打 tag。
- 无传递依赖、无版本约束求解，声明是扁平的，一个技能一条记录。
- 无 skill 内容安全扫描，也无遥测。

## 许可证

skillmod 基于 [MIT License](LICENSE) 发布。
