# Octopus UI 证据与判断层级

固定提交：`8aa58b0340ba2de58ce74fbfacd32597c44842a2`。以下区分“已人工看图”和“由 UI 源码推断”。

## 已人工看图的实际画面

### desktop-home.png

永久链接：https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-home.png

- 标题和 logo 居左，左侧为悬浮圆角 Dock，主内容在有限宽度内居中。
- 暖灰背景上有柔和绿色光斑；白色大圆角卡片、极浅描边、绿色图标形成统一层次。
- 顶部四组指标卡以竖分隔线分区，指标值字号明显大于标签，适合快速扫读。
- 热度方格、面积图、供应商排行榜采用同一绿色序列；图表弱化网格线，只保留必要的坐标和阈值。
- 视觉气质是“个人运营仪表盘”，信息密度不低但留白充分。

### desktop-channel.png

永久链接：https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-channel.png

- 顶部只有“渠道”、搜索和新增；渠道以 4 列卡片排列。
- 卡片用状态胶囊、请求次数、总成本三块核心信息，避免列表表格把配置细节一屏铺满。
- 卡片高度和图标位置一致，网格节奏清楚；左侧 Dock 的渠道图标高亮。
- 由于页面列出 10 个渠道后仍留白，桌面布局不需要无限密度；移动端是否仍使用卡片仍需另看 mobile 截图/源码。

### desktop-group.png

永久链接：https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-group.png

- 两张纵向分组卡片并排；标题、复制、删除、添加位于一行。
- 成员按编号、拖拽点、模型、供应商排列，显示顺序优先级。
- **证据冲突**：截图还显示“轮询 / 随机 / 故障转移 / 加权分配”四个模式，而当前源码只有 `manual / failover`。这说明固定提交中的展示 PNG 已落后于实现。该图只能用于布局判断，不能用来宣称当前路由支持轮询/随机/加权。

### desktop-log.png

永久链接：https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-log.png

- 日志是宽而扁的卡片流，单行兼顾状态、时间、首字、总耗时、输入/输出 token、费用。
- 供应商图标和名称作为第一视觉锚点，之后才是模型名，符合“先定位谁服务、再看请求结果”的排障顺序。
- 不同状态只用轻量颜色和图标区分，没有大面积红绿背景；色彩不会盖过文本。
- 卡片横向信息多，桌面端有效；小屏需要允许换行或折叠，不能直接压缩字体。

### mobile-home.png

永久链接：https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/mobile-home.png

- 顶部为 logo/标题，内容纵向重排；指标卡和图表都有充足圆角、内边距。
- 底部是悬浮图形 Dock，与桌面左侧 Dock 保持同一交互心智。
- 热度格、图表和排行在单列中按相同卡片宽度排列，避免横向滚动。

## 由源码推断、未做浏览器交互实测

- 固定导航：首页 home、渠道 channel、分组 group、模型 model、日志 log、设置 setting。
  来源：`web/src/components/app-shell.tsx`、`web/src/stores/app.ts`。
- 桌面 Dock 与移动底部 Dock 的响应式切换，以及悬停/触摸预加载页面，可从 app shell/page preload 源码读出；真实动画流畅度和低端设备性能未测。
- 渠道编辑器是分步表单：preset、connection、keys、grants、advanced，降低长表单一次性认知负担。
  来源：`web/src/components/modules/channel/Form.tsx`。
- Key 编辑支持命名、掩码、独立开关、独立探测。
  来源：`web/src/components/modules/channel/FormKeys.tsx`。
- Grant 授权矩阵有三态批量设置、展开全部/折叠、清空，并按模型 × Key × 协议组织。
  来源：`web/src/components/modules/channel/FormGrants.tsx`。
- 分组候选按“渠道 → 模型 → Key”三级选择，展示协议标签，并能按分组名称自动匹配候选。
  来源：`web/src/components/modules/group/ItemList.tsx`。
- 分组支持拖拽排序、置顶/置底、键盘操作、删除二次确认；成员显示冷却和亲和倒计时。
  来源：`web/src/components/modules/group/Editor.tsx`、`MemberStatus.tsx`。
- 日志详情可折叠 JSON，显示逐轮错误、实际模型/协议/渠道 Key、首字与耗时、手动取消；manual 模式允许人工切换成员。
  来源：`web/src/components/modules/log/Item.tsx`。
- 首页有请求/成本/token 指标切换、today/7d/30d 范围、渠道榜与模型榜、隐藏渠道名和分享 PNG。
  来源：`web/src/components/modules/home/index.tsx`、`rank.tsx`、`metric-tabs.tsx`、`store.ts`。
- 主题提供 light/dark/system；语言资源固定为 English、简体中文、繁体中文。
  来源：`web/src/provider/theme.tsx`、`web/src/provider/locale.tsx`、`web/src/locales/`。
- VirtualizedGrid 用于渠道/分组卡片的三档响应列数（1/2/3），说明数据量大时希望避免普通网格一次挂载过多 DOM。
  来源：`web/src/components/common/VirtualizedGrid.tsx`。
- CSS 使用暖灰背景、绿色主色、深绿暗色主题，并明确设置 focus-visible、ARIA 和键盘激活状态；未找到足以证明完整 WCAG 合规、reduced-motion 全面处理或屏幕阅读器实测的证据。

## UI 结论

Octopus 的强项是把复杂路由对象转成可扫读的运营界面：授权矩阵负责“配置能力”，分组候选负责“建立优先级”，日志卡片负责“证明本轮实际走了什么”。弱点是展示素材维护不一致，旧模式仍留在截图中；此问题会直接误导评估和用户。