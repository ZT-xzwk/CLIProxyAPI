# Fork 改造记录（CLIProxyAPI）

本文件只存在于本 fork（`ZT-xzwk/CLIProxyAPI`），上游 `router-for-me/CLIProxyAPI` 没有。
记录 fork 内部改动的落地位置与合并上游时的复核点。

**当前状态：业务代码零改动，只有 CI 相关的 fork 自有提交。**（截至 2026-11-12）
第一次真正改代码时，请在「改造清单」加一行，并在「变更记录」写日期条目。

> 给 AI 代理的入口约定写在上游同款 `AGENTS.md` 的最后一节「Fork Conventions」，
> 该节是本 fork 唯一修改的上游文件内容，合并时需要留意（见第 5 节）。

---

## 1. 分支与远程

| 分支 | 角色 |
| --- | --- |
| `main` | 跟随上游，不承载改造；不要在 `main` 上提交 |
| `dev/personal` | 唯一改造分支（当前工作分支）；fork 镜像也只从这里构建 |

| 远程 | 地址 | 说明 |
| --- | --- | --- |
| `origin` | `https://ghfast.top/https://github.com/ZT-xzwk/CLIProxyAPI.git` | fork 镜像 |
| `upstream` | `git@github.com:router-for-me/CLIProxyAPI.git` | 上游，push 为 `no_push`（只读） |

上游还有 `upstream/dev`、`upstream/legacy`、`upstream/release/*` 等分支，本 fork 跟随 `upstream/main`。

> 已知事实：本地 `upstream/main` ref 停在 `dbb70621`（2026-10-30），而 `dev/personal`（`3cba1041`，2026-11-12）
> 已包含合并进来的更新。也就是说**本地上游 ref 是陈旧的**，合并前务必先 `git fetch upstream`，否则会误判差距。

若 `origin` 因凭据被拒，可直接推到真实仓库（本机 git global 有 `insteadof` 把 https 改写为 SSH，需要压掉）：

```bash
GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null \
GH_CONFIG_DIR="C:/Users/zt/AppData/Roaming/GitHub CLI" \
git -c credential.helper='!gh auth git-credential' push \
  https://github.com/ZT-xzwk/CLIProxyAPI.git dev/personal
```

---

## 2. 改造清单

| # | 改造 | 提交 | 涉及文件 | 上游是否同文件 | 合并复核要点 |
| --- | --- | --- | --- | --- | --- |
| 1 | fork 镜像构建（推 GHCR `ghcr.io/<owner>/cli-proxy-api-fork`） | `e1bbcf24` | `.github/workflows/build-fork-image.yml`（fork 新增） | 否 | 上游若新增同名 workflow 需合并；`paths-ignore` 含 `**/*.md`，纯文档提交不触发构建 |
| 2 | 构建触发分支由 `main` 改为 `dev/personal` | `7ca4d489` | 同上 | 否 | 确认触发分支仍是 `dev/personal`，`workflow_dispatch` 仍保留 |
| 3 | `AGENTS.md` 末尾追加「Fork Conventions」小节 | 本次提交 | `AGENTS.md` | **是**（上游文件，本次只追加末尾一节） | 上游经常改这个文件；合并时若冲突，保留上游全文 + 只在末尾重贴本 fork 小节 |

业务功能改造：**暂无**。

---

## 3. 合并上游的操作步骤

```bash
# 1) 先更新上游引用（本地 ref 陈旧，不 fetch 会误判）
git fetch upstream --prune

# 2) 看差距与提交意图，不要只看 --stat
git rev-list --left-right --count upstream/main...dev/personal
git log --oneline dev/personal..upstream/main

# 3) 合并（在 dev/personal 上做）
git merge upstream/main

# 4) 验证（Go 项目的完整基线）
gofmt -l .            # 应无输出
go build ./...
go test ./...
```

Docker 相关改动另跑 `./docker-build.sh`（Windows 用 `docker-build.ps1`）。

---

## 4. 逻辑冲突审查（Go 项目特有）

合并可能「无文本冲突 + `go build`/`go test` 全绿」，但上游已把同一段语义改写。合并后逐条确认上游的硬约束是否仍然成立
（这些规则原文在上游 `AGENTS.md` 的 Code Conventions 一节，本 fork 不得偏离）：

- **thinking 管线**：`internal/thinking/` 的「canonical `ThinkingConfig` → per-provider 翻译」架构不能被绕过；
  合并后若上游改了 `ApplyThinking` 的入参或顺序，本 fork 的相关改动要跟着对齐。
- **JSON / Base64**：必须用 `github.com/MoeForge/moego/ojson` 与 `moego/base64`；
  禁止 `tidwall/gjson`、`tidwall/sjson`、标准库 `encoding/base64`。写操作必须在 `*ojson.Doc` 上完成、只在出口序列化一次。
- **payload finalizer 顺序**：内置翻译/规范化/注入/清理 → 用户 payload 规则只应用一次 → 之后只允许传输层 framing、
  序列化、签名、只读校验。任何「后面又覆盖配置值」的逻辑都算违规。
- **translator**：`internal/translator/` 不允许单独改动，只能作为更大改动的一部分。
- **超时**：只允许出现在凭证获取阶段；上游连接建立后不得再加超时（仅 `codex_websockets_executor.go`、
  `wsrelay/session.go`、management `api_tools.go`、`cmd/fetch_antigravity_models` 例外）。
- **`/v0/management`**：已废弃，除修复编译错误外不要动。
- **日志/错误**：不用 `log.Fatal*`，用 logrus 结构化日志，不泄漏 token。

合并后额外检查：

- [ ] `.github/workflows/build-fork-image.yml` 仍存在且触发分支为 `dev/personal`
- [ ] `AGENTS.md` 末尾的「Fork Conventions」小节仍在（上游改这个文件时最容易丢）
- [ ] `go.mod` 的 Go 版本与 CI 里的 `go-version-file` 仍一致
- [ ] 若上游新增了 `internal/**` 下的 init 逻辑（如 registry updater、watcher），确认不影响本 fork 的部署形态

---

## 5. 环境坑

- `upstream` 是 SSH 地址且 `no_push`，沙箱里常常读不到 `~/.ssh`，只能 fetch 不能手推——推送走 `origin` 或上面的直连方式。
- 本机 git global 配了 `url.git@github.com:.insteadof https://github.com/`，凡是 `remote add/set-url` 都会被改写成 SSH，
  需要前缀 `GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null`。
- 本地 Go 工具链是 1.27.x，`go.mod` 声明 1.26.10；不要为了「顺手升级」改 `go.mod` 的版本行。
- 提交信息在 bash 里用 heredoc（`git commit -F - <<'EOF'`）；PowerShell 的 `@'...'@` 会被 bash 当成字面 `@`。
- `.md` 改动不触发镜像构建（`paths-ignore`），可以放心只推文档。

---

## 变更记录

### 2026-11-12
- 建立本文件：补齐分支与远程、改造清单（目前仅 CI 两项 + `AGENTS.md` 追加节）、合并 SOP、Go 项目的逻辑冲突红线、环境坑。
- 在 `AGENTS.md` 末尾追加「Fork Conventions」小节（英文，遵守该文件的 "Comments in English only" 约定），
  指向本文件作为 fork 改造的真相来源。
- 确认当前状态：业务代码零改动，`dev/personal` 领先本地 `upstream/main` ref，但该 ref 陈旧（`dbb70621`，2026-10-30），
  真实差距需 fetch 后重算。
