# 浏览器辅助与轻量导入桥接验证记录

日期：2026-11-23，Asia/Shanghai。对应[设计](../specs/2026-11-23-browser-import-bridge-design.md)、[实施计划](2026-11-23-browser-import-bridge.md)与[使用说明](../../browser-import.md)。

## 源码与验收范围

从主目录完整当前源码建立隔离工作区 `codex/browser-import-bridge`，HEAD 保持 `90aee95761d8fa21ab3f292b3b2930f8fa24be08`。基线包括此前全部未提交阶段改动，1,089 个文件、26,052,412 字节；本阶段差异按完整基线 ZIP 生成，不以 HEAD 的全部差异代替阶段范围。

基线 ZIP SHA-256：`74fed36a5560e03f6290398e0bc26c3842443bb3649cdb3de8d60db87aececa2`。原运行程序、9 个保护资源、技能链接和主依赖仅比较哈希／链接身份，不读取真实 env、数据库或秘密。

实现覆盖：严格文件规范化与安全检查、只读有界验证和真实响应身份、去重与明确目标、窄范围凭证修复、固定预览／事务／未知恢复、浏览器助手产物、连续工作页、账号与渠道入口。数据库 schema、版本与部署状态保持原有机制；本次没有提交、推送、发布、安装依赖或启动生产网关。

## 已观察到的 RED 与纠正

| 场景 | 初始事实 | 处理与定向结果 |
| --- | --- | --- |
| 检查和预览路由 | 新接口返回 404；预览组 24 个失败事件 | 注册接口并实现完整流程；审查修复后的完整桥接／共享预览 136 个 pass 事件，0 fail，18.511 秒 |
| 原始 UTF-8 | Go JSON 和浏览器默认文件读取会替换坏字节 | 服务端原字节校验、浏览器 fatal TextDecoder；保留中文和重复键，损坏凭证明确拒绝 |
| 真实账号身份 | 初始 10 fail／11 pass，缺响应 ID、Sub2API 小数 ID | 身份只来自真实 profile；21 个 pass 事件，3.230 秒 |
| 凭证修复与历史 | 泛用账号更新未替换密文，且级联停用渠道；补充回归再次失败 | 专用 `RepairSiteAccountCredential`，保留开关、历史、模型、绑定和渠道；真实 SQLite 事务与触发器验证回滚 |
| 前端新契约 | 原测试 282 pass，三个新模块／元数据失败 | 核心 297 console／2 auth，后续文件解码与入口补充到 298 console／2 auth；双 TypeScript 与 Vite 通过 |
| 真实浏览器入口 | 缺导航和 h1，route-exists 0/1 | GREEN 1/1，零异常事件，owned static server 已关闭 |
| Smoke 夹具 | 初轮不匹配凭证嵌套结构、预览到期、定位过宽或模拟状态不符合产品契约 | 按真实 DTO 和生命周期修正夹具，保持实际安全边界；失败与补跑证据全部保留 |
| 切换来源后凭证显隐 | 主凭证已显示后切换条目，输入仍为 text；实际浏览器 RED 0/1 | 两处 SecretInput 按所选条目重新挂载，GREEN 1/1，重建后完整浏览器 39/39 |
| 同主凭证的辅助资料修复 | Refresh Token／有效期改变仍被判为无变化；RED 14 fail／3 pass | 比较实际刷新资料与 EffectiveExpiresAt，验证后重判冻结凭证；显式目标与验证仍必需，20 pass |
| 旧版本软删除名称 | 修正夹具后 RED 3 fail／1 pass；当前 DeleteSite 实际是物理删除 | 真实 SQLite 旧墓碑名称可复用，不复活旧站点；指纹仍阻止并发名称变化，5 pass |
| JSON surrogate 转义 | ASCII 文件的孤 surrogate 被解码成 U+FFFD，RED 21 fail／16 pass | 原字节和转义共同校验；两接口／文本和对象、合法 pair／字面 U+FFFD／反斜线 u，43 pass |
| 等价 URL 编码 | unreserved 与 percent-escape 被拆成不同站点，RED 6 fail／1 pass | unreserved 归一、其他字节保留 %XX；真实匹配／去重及 %2F／完整前缀／大小写／点路径回归，26 pass |
| 刷新资料空值与类型 | null／非法类型可能被当作清除，RED 6 fail／1 pass | null 按省略保留，错误类型在 Provider 前拒绝；Hub 2.0／3.0／4.0 的更新也通过完整 136 项定向复验 |
| 令牌模型快速翻页 | 首次 Main 共享检查 44/45；控制时钟确认空搜索初始化回调在第 2 页后触发，GET offset 0→50→0 | 仅搜索词实际改变时安排页码重置；新增确定性 RED 0/1→GREEN，实际新搜索仍生效。两项定向与完整 46 项共享／39 项导入检查通过 |

## 最终检查

工作区修复后的验证、独立复审和当前主目录验收已通过。本阶段本地整合与归属清理完成，实际执行和收尾材料记录如下。

| 检查 | 实际结果 | 证据 |
| --- | --- | --- |
| 完整内部 Go | 35 个有测试包、5,079 个含子测试 pass 事件、6 skip、0 fail；审查修复、测试 lint 微调前的完整运行 281.201 秒 | `task4-review-final-internal-result.json` 与完整 JSON 日志；此前 5,001 项的冻结结果也保留 |
| lint | 0 issues，76.425 秒；两处新测试静态检查问题已修正 | `task4-review-test-lint-corrected-result.json`；失败日志保留 |
| 桥接／共享预览复验 | 136 pass、0 fail；测试 lint 修正后复跑 35.165 秒 | `task4-review-after-test-lint-green-result.json`；初次完整 GREEN 18.511 秒也保留 |
| Go embed 构建 | 最新前端资源内嵌成功，3.427 秒；产物写入归属临时目录且未运行 | `task4-paging-race-embed-result.json` |
| 前端 | 298 console／2 auth，0 fail；掩码与快速翻页修复后的双 TypeScript／Vite 通过 | `task4-paging-race-frontend-green-result.json` |
| 桥接实际浏览器 | 最新构建 39/39，0 unexpected events，server_closed=true | `task4-paging-race-browser-full-green/result.json` |
| 共享操作回归 | 新增快速翻页竞态回归后 46/46，0 unexpected events，server_closed=true | `task4-paging-race-accessibility-full-green/result.json`；初次 Main 44/45 失败证据保留 |
| 账号／渠道补充回归 | 桌面和手机各覆盖普通账号显隐／创建／手动同步及原有渠道 Key 编辑，4/4；0 unexpected events，server_closed=true | `task4-affected-ui/result.json` |
| 主目录关键后端 | 桥接／共享预览 136 pass、0 fail，26.072 秒；补充整合没有改变 Go／模块文件 | `task4-main-preview-final-result.json`、`task4-pagination-followup-source-check.json` 与 `acceptance.json` |
| 主目录完整界面 | 共享操作 46/46、浏览器导入 39/39；均 0 unexpected events、server_closed=true | `task4-main-accessibility-final-3/result.json`、`browser-import-main-final-3/result.json` |
| 文档与保护 | 主目录 12 份文档、156 条本地链接通过；9 个保护资源、原技能链接和主依赖无变化 | `task4-main-document-links-final-3.json`、`acceptance.json`、`cleanup.json` 与基线 manifest |
| 归档与清理 | 清理前 8 个 ZIP 的 CRC／逐文件 SHA-256 通过，462 份执行证据归档；移除 2,592,431,088 字节的逻辑文件大小 | `pre-cleanup-archive-verification.json`、`evidence-manifest.json` 与 `cleanup.json` |

6 个测试跳过：Windows 不保留 POSIX 权限的两个计费 journal 权限测试；缺少原生 Gemini/Grok 采样语料的三个签名测试；未配置 DSN 的 MySQL 集成测试。另有 5 个内部包没有测试文件，未作为有测试包计数。没有为补齐这些项目下载语料、配置真实数据库或扩大权限。

完整内部 Go 后仅修改一份新回归测试：移除未使用的初始绑定读取、把 if 分支转为 tagged switch。`task4-test-lint-source-delta.json` 和 `task4-pre-test-lint` 快照确认后端功能源码完全相同；当前测试源码随后通过 136 项定向检查、零问题 lint 和 embed 构建。后续快速翻页修复只改 TokenForm、共享浏览器用例和编译资产，`task4-pagination-followup-source-check.json` 确认全部 Go／模块文件保持一致；前端变化由重建后的完整套件验证。

`account_flow_smoke.py` 和 `sites_ui_smoke.py` 属于需要真实服务／登录的旧入口，本阶段未运行。相关账号／站点／渠道行为通过现有合成夹具和新增有归属的受影响 UI 脚本验收；调用被限制到本次静态服务，真实上游封锁。

浏览器执行真正下载的自包含助手脚本，核对访问令牌、Sub2API 刷新字段与手工 Cookie 导出；多候选需要明确选择，错误 origin／iframe 在存储读取前拒绝。检查有限存储键、受限存储、导出和关闭清理、焦点恢复与零网络请求。控制台覆盖文件、分页、来源确认、目标修复、取消和迟到响应、应用响应丢失、未知结果禁止替代、人工核对移除、安全 URL、账号入口、渠道预选和停用提示。

5 个 JPEG 可见视口为 320×900、390×844、768×1024、1280×900、1280×480；已人工检查布局，无横向溢出，最终主目录的 320px 手机和短桌面截图也已核对。最新构建实际路由 chunk 为 32,203 decoded／11,301 gzip 字节，控制台会话 40 请求、355,914 encoded 字节，均低于 35 KiB／450,000 字节预算；共享可达性套件的 400,000 字节／45 请求预算也通过。

## 独立审查与本地整合

初轮阶段审查以 `task4-review/phase.diff` 和完整基线为准，78 个变更文件、25 个过时生成文件；不是全部 HEAD 差异。新上下文审阅者只读源码和已有证据，结论保存为 `final-independent-review.md`，确认 1 Important／3 Minor，均已复现并修复，原审查保留。修复增加两份后端回归文件，最终阶段快照重新生成；同一审阅者的有界复审另写 `final-independent-review-followup.md`。初轮报告引用的 38 项浏览器结果已由实际重建后的 39 项结果取代，不能把旧报告中的待验证状态当作最新执行结果。

最终 `task4-final-2` 已整合到主目录，1,115 份源码与隔离工作区、快照逐文件一致。相对完整基线有 81 个变更文件、25 个过时生成资产，1,034 份未涉及文件保持原样。首次整合的被替换原件保存为 `pre-integration-originals.json/.zip`；快速翻页修复的补充整合另存 `followup-task4-final-2/originals.json/.zip`，实际补充 31 个变更文件、移除 26 个中间生成资产。两次原件均通过 CRC 和逐文件哈希校验，额外补充原件的 `hashes.json` 也单独核验。

`final-independent-review-followup.md` 确认初轮四项问题全部关闭，未发现新增 Critical／Important 或具体回归；静态审查与执行验证的职责分别记录。复审额外说明首次取得真实 UserID 时要求显式目标，是确认步骤而非自动更新，已写入使用说明。该复审核对了功能／前端源码与 pre-test-lint 快照的逐文件哈希、两处测试微调和最新 136 项／lint／embed／39 项浏览器证据。

首次 `task4-final` 整合后，Main 后端 136 项和导入浏览器 39 项通过，共享检查 44/45 揭示既有令牌模型页码的初始化竞态。`permission-paging-controlled-red/permission-diagnosis.json` 用原页面与控制时钟记录第 2 页后初始化回调触发、空查询读取 0→50→0；自然单项复跑通过也保留，不能用它掩盖竞态。工作区新增确定性用例，观察 RED 后只在实际查询改变时安排重置，并加强原选择用例对搜索结果本身的等待。完整 46 项／39 项及前端、embed 通过后，按已整合快照对比补丁并归档中间原件，再更新 Main；此额外 UI 修复由行为回归验证，原有四项独立复审记录范围保持清楚。

补充整合后的第一次 Main 共享复验为 45/46：`details-1440-900-light` 的截图触发原有 5 秒超时，页面无 JavaScript 或异常请求事件。单场景诊断先在另一处已可见的渠道详情点击发生超时，随后在完全相同源码与限制下通过，三张截图分别耗时 0.148／0.406／0.274 秒。没有修改源码或放宽限制；完整原条件复跑得到共享 46/46、桥接 39/39 和文档 12／156。具体浏览器或主机组件导致的延迟未定位，原失败、两次单项诊断、主机负载观察和最终通过分别保留在 `task4-screenshot-timeout-investigation.json`、`task4-screenshot-timeout-confirmation.json` 及其引用材料中。

## 证据与边界

归档目录：`E:/Dev/tools/api/archive/browser-import-bridge-2026-11-23`。完整基线、阶段源码、两次被替换原件及 462 份逐次结果 JSON、完整日志、JPEG、实际下载助手和固定公开 schema 参考已归档。执行证据 ZIP 为 10,817,030 字节，SHA-256 `010be138162bd11dcb1d0bbae85b6747423ef6abc24b60ca67b9bf9585bf0da7`；8 个清理前 ZIP、辅助脚本及两份原审查报告的哈希均通过。收尾说明变更与完整最终状态由 `final-state/`、`post-cleanup-document-links.json` 和 `final-verification.json` 记录。

`cleanup.json` 确认仅移除本阶段工作区 `browser-import-bridge`、同名分支和外置 `.tmp-browser-import-bridge`。借用的 `console/node_modules` junction 非递归解除，主依赖目标、原技能链接、9 个保护资源与 HEAD 保持一致；清理后 Main 仍精确匹配验收快照。

| 已移除内容 | 逻辑字节数 |
| --- | ---: |
| 隔离工作区 | 26,577,780 |
| 本阶段 Git 工作区元数据 | 88,125 |
| 外置缓存与验证临时目录 | 2,563,586,267 |
| 两次整合的过时构建资产 | 2,178,916 |
| 合计 | **2,592,431,088** |

合计约 2.59 GB／2.41 GiB，为已移除文件的逻辑大小，归档保留量另计。清理后只更新实施计划、本记录和两份研究进度文档，功能源码保持验收版本。

All API Hub 参考固定到 `5c4101d93143df6b3b112ccd819a29f3f4ada78c`，仅核对公开账号／备份格式及浏览器认证字段，未复制其 AGPL 产品实现。已确认的 14 项改进主线已完成 13 项实现与本地验收；下一项为 P2 可选请求缓存，本阶段没有开始缓存实施。

这些测试不替代真实上游、生产浏览器、HttpOnly Cookie、Sub2API 浏览器私钥证明、MySQL/PostgreSQL、跨实例租约或部署验收，也不把模型列表／profile 读取成功写成实际推理、签到或完整能力验证。
