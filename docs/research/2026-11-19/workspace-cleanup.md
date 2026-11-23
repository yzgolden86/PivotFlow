# 工作区整理与空间回收记录

日期：2026-11-19。工作区：`E:/Dev/tools/api`，主仓库：`PivotFlow/`。整理先建立清单与文件哈希，再区分正式内容、可重建产物和需要追溯的历史材料。未修改网关产品代码，也未替换本机二进制或用户数据。

## 1. 空间与文件数

| 指标 | 整理前 | 整理后 | 变化 |
| --- | --- | --- | --- |
| 普通文件内容大小 | 6,141,501,920 bytes，约 6.14 GB | 约 0.32 GB，包含全部归档、研究证据和验证日志 | **净回收约 5.82 GB（5.42 GiB）** |
| 普通文件数 | 62,432 | 约 1.08 万 | 减少约 5.17 万 |
| 主项目追踪文件 | 802 | 原有文件全部保留 | 变化仅限 8 份文档/忽略文件 |
| 保护文件 | 8 | 8 | SHA-256 全部一致 |

精确整理后大小与文件数见 [after-inventory.json](../../../../archive/cleanup-2026-11-19/after-inventory.json)，对应基线见 [before-inventory.json](../../../../archive/cleanup-2026-11-19/before-inventory.json)。最后写入报告会带来少量字节变化，表中 GB/MB 为四舍五入，原始清单才是精确测量。

这里统计文件逻辑大小，没有扫描工作区外的系统目录、全局模块缓存或磁盘其他项目；也没有把本轮额外创建又删除的临时资料算成旧工作区空间回收。清理前盘点期间 Go 缓存有过增长，最终净回收使用已保存清单与完整整理后工作区计算，不能把不同阶段缓存大小直接相加。

## 2. 清除的可重建内容

| 类别 | 处置 | 证据 |
| --- | --- | --- |
| Go 缓存/旧构建验证目录 | 清除 38 个临时与缓存目录，清单时约 5,561,617,090 bytes / 40,242 files | [删除记录](../../../../archive/cleanup-2026-11-19/removed-first-batch.json) |
| 旧 UI smoke/demo/截图/重复依赖 | 随各任务目录清理；有用脚本和调试数据先归档 | [保留资料](../../../../archive/cleanup-2026-11-19/retained-debug-files.json) |
| 参考项目解压副本 | 删除六份完整验证的解压目录，包括两个相同 ccLoad 4.10.2 副本 | [参考清理记录](../../../../archive/cleanup-2026-11-19/removed-reference-sources.json) |
| 根目录参考 ZIP 副本 | 四份原 ZIP 已完整复制到 archive 后删除根副本 | 同上；[归档 SHA](../../../../archive/reference-snapshots/archives.json) |
| `.release-tools` | 删除约 115 MB 的工作区旧工具与临时下载，保留 notices 与历史脚本 | [工具记录](../../../../archive/cleanup-2026-11-19/removed-legacy-tools.json) |
| 本轮写入探针/浅克隆 | 完成报告后清除探针和 Octopus 临时只读克隆，关键源文件与图另存 | [探针](../../../../archive/cleanup-2026-11-19/removed-write-probes.json)、[本轮临时记录](../../../../archive/cleanup-2026-11-19/removed-research-temp.json) |

38 个目录中包括根 `.tmp-gocache`、`.tmp-demo`、`.tmp_screens`，以及 PivotFlow 的 `.tmp-*` / `.tmp` / `.gocache`。不是按名称盲删：临时脚本、DB/WAL/SHM/主密钥先形成可验证归档，正式 `data/` 不在删除目标内。

旧工作区工具副本为 golangci-lint 2.12.2、gh 2.100.0；本机已安装版本分别为 2.13.2、2.101.0，位置 `C:/Users/80470/go/bin`。删除重复副本前确认替代工具存在，未修改全局安装。

## 3. 有用历史资料的归档

### 调试源码与测试数据

311 个临时脚本/源码/测试数据库及关联文件，原始 78,140,454 bytes，压缩为 **14,298,572 bytes**：

[retained-debug-sources-and-datasets.zip](../../../../archive/cleanup-2026-11-19/retained-debug-sources-and-datasets.zip)。

删除原目录前执行 ZIP CRC 与逐项 SHA-256；文件路径相对于工作区保留。数据库及其 `-wal`、`-shm` 和 `fusion-master.key` 保持原有目录关系，可以取回同一组历史开发数据。归档属于本地私有开发资料，放在主 Git 仓库之外。

### 参考源码

[reference-snapshots](../../../../archive/reference-snapshots/README.md) 保留六个压缩包：ccLoad 4.10.2、ccLoad 4.9 本地目录、All API Hub 3.61.0、All API Hub 旧 main、本地 metapi-main、octopus-dev。总计约 90.59 MB，原包自带许可证和来源说明保留。

六份解压副本逐文件比对没有差异、额外修改或缺失文件；同一 ccLoad 4.10.2 的两个副本内容完全相同。旧 main 包中 All API Hub 实际为 3.55.0；octopus-dev 是 Hureru fork 的旧开发快照，不能代表 bestruirui 最新主线。

证据：[snapshot-verification.json](../../../../archive/reference-snapshots/snapshot-verification.json)。在研究结束、删除剩余解压目录前，又复核三份目录合计 9,092 文件，仍无差异：[删除前复查](../../../../archive/cleanup-2026-11-19/reference-pre-removal-recheck.json)。

### 规划与会话资料

13 个历史条目按用途移动：

- `archive/planning/`：最初融合设计与第一阶段实施说明。
- `archive/session-history/`：四份旧会话摘录、故障日志、构建日志、记忆备份、旧发布验证脚本、Git 诊断和两个测试摘要。

原路径/目标/文件哈希记录在 [moved-history.json](../../../../archive/cleanup-2026-11-19/moved-history.json)，没有把这些资料混入当前产品文档。

## 4. 保留与完整性

| 保留对象 | 原因与验证 |
| --- | --- |
| 主仓库源码、脚本、测试、Git 历史 | 802 个原追踪文件全部存在，除列出的文档/忽略规则外哈希一致 |
| `web/console`、`web/auth` | 正式静态资源，会嵌入 Go 发布内容 |
| `www/` | 独立介绍站，有自己的职责 |
| `console/node_modules` | 当前前端开发依赖约 115.7 MB，保留以继续开发；进入 Docker 构建上下文时排除 |
| `data/` 与主密钥 | 正式用户数据，所有基线文件 SHA-256 一致 |
| `pivotflow.exe` | 仍保留当前文件，SHA-256 一致；它是 v0.4.8 构建，报告明确提示版本差 |
| 根 `key.txt`、`env` | 用途未完全确认且体积很小，保留原位置、哈希一致，未读取/展示内容 |
| `.claude`、`.workbuddy-ai` 与长期记忆 | 保留现有本机代理状态，仅旧记忆备份移至 archive |
| 未挂载旧前端源码 | 少量追踪文件仍可能含共享导出或回滚价值；本轮登记候选并保留，没有与临时缓存一起删除 |

最终 [final-verification.json](../../../../archive/cleanup-2026-11-19/final-verification.json) 保存原追踪文件/保护文件结果与七个 ZIP 的 CRC 检查。可重跑 [verify_cleanup.py](../../../../archive/cleanup-2026-11-19/verify_cleanup.py)，它只读文件并写整理后清单，不删除/移动，不覆盖 before/baseline。

## 5. 当前目录

```text
E:/Dev/tools/api/
├── WORKSPACE.md                     工作区入口
├── PivotFlow/
│   ├── internal/ console/ web/       产品源码与正式前端
│   ├── docs/research/2026-11-19/     本轮项目、UI、竞品与改进报告
│   ├── scripts/                     可复用验证脚本
│   ├── data/                        原用户数据与主密钥
│   ├── pivotflow.exe                保留原二进制，版本已提示
│   └── .git/                        原提交历史
├── archive/
│   ├── cleanup-2026-11-19/          清单、哈希、调试资料与验证记录
│   ├── reference-snapshots/         压缩参考源码
│   ├── planning/                    早期设计
│   └── session-history/             旧会话与日志
├── .claude/ .workbuddy-ai/           保留本机状态
└── key.txt env                      原位置保留
```

根目录已经不再散放完整参考源码、ZIP、构建工具和历史会话文件。入口：[WORKSPACE.md](../../../../WORKSPACE.md)、[本轮研究](README.md)。

## 6. 顺便纠正的说明与今后的规则

变化仅限 `.gitignore`、`.dockerignore`、`CLAUDE.md`、两个 README、文档入口、配置说明和故障排查：

- Git 补齐 `.tmp/` 和 `.tmp_*/`；Docker 排除 `.tmp*/`、Go 缓存、console 依赖和本机 exe。
- 模型映射和日志刷新间隔已热生效，文档纠正旧重启要求。
- 当前后台更新只读检查、免费/负倍率和站点价格口径按代码说明；费用软限额明确考虑并发。
- Octopus 上游和 Hureru fork 同时注明，文档图标明旧演示版本。
- 添加项目/竞品/清理研究入口，没有修改产品功能或提交/推送。

后续临时产物集中 `.tmp-<任务>/`；完成后保留可复用脚本到 scripts，保留合成截图与研究到 docs，其余任务产物清除。Go 缓存不必每个 smoke 都另建一份永久目录，参考源码只保留一个有 SHA 的压缩快照。删除前用原生 PowerShell 核验完整目标位于指定工作区，避开正式数据和 reparse point，建立需要保留的归档并验证。

本轮前端 TypeScript 检查与 167 项测试通过；后端 33 个有测试的内部包最终通过，另有 5 个包无测试文件。首次依赖下载失败、完整运行的两分钟应用包超时和默认十分钟上限的单独成功补跑均保留，详见 [项目现状](pivotflow-current-state.md) 与 [Go 验证汇总](../../../../archive/cleanup-2026-11-19/verification-logs/go-test-summary.json)。

任务内临时 Go 模块与编译缓存合计 1,040,098,556 bytes / 20,832 files 已全部清除；仅保留六份日志/结果和汇总。新建又删除的 1.04 GB 不另外累加到旧工作区的净回收量，见 [本轮缓存删除记录](../../../../archive/cleanup-2026-11-19/removed-final-verification-temp.json)。
