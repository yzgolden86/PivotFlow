import { Activity, FileClock, KeyRound, Route, Settings } from 'lucide-react'

const tools = [
  { title: '渠道编辑与凭证', description: '渠道、Key、URL、模型映射和 OAuth 凭证导入。', href: '#/channels', icon: Route },
  { title: '下游 API 密钥', description: '访问令牌、渠道限制、调用统计和有效期。', href: '#/tokens', icon: KeyRound },
  { title: '活动请求与调试', description: '查看进行中请求和调试捕获快照。', href: '#/logs?view=active', icon: Activity },
	{ title: '系统设置', description: '运行参数、余额和签到失败通知。', href: '#/system', icon: Settings },
  { title: '消费趋势', description: '请求、Token 和费用趋势。', href: '#/trend', icon: FileClock },
]

export default function AdvancedPage() {
  return <div className="workspace-page advanced-page"><header className="page-header"><h1>工具</h1></header><section className="advanced-list" aria-label="工具列表">{tools.map(({ title, description, href, icon: Icon }) => <a href={href} key={title}><span><Icon size={18} /></span><div><strong>{title}</strong><p>{description}</p></div></a>)}</section></div>
}
