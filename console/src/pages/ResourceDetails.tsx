import type { ReactNode } from 'react'
import { Modal } from './siteShared'

export interface ResourceFact {
  label: string
  value: ReactNode
}

export default function ResourceDetails({ title, facts, close }: {
  title: string
  facts: ResourceFact[]
  close: () => void
}) {
  return <Modal title={title} close={close} wide>
    <dl className="resource-details">
      {facts.map(({ label, value }) => <div className="resource-detail-fact" key={label}>
        <dt>{label}</dt><dd>{value ?? '未知'}</dd>
      </div>)}
    </dl>
  </Modal>
}

export function DetailURL({ value }: { value?: string }) {
  if (!value) return <>未配置</>
  let display = value
  let credentialsHidden = false
  let link = false
  try {
    const url = new URL(value)
    // URL 用户信息可能是代理/上游凭证。详情不扩展凭证的展示范围。
    if (url.username || url.password) {
      url.username = ''
      url.password = ''
      display = url.href
      credentialsHidden = true
    }
    link = ['https:', 'http:'].includes(url.protocol)
  } catch {
    // 无效的主机仍可能带有 userinfo；保留地址文本前先去除该部分。
    display = value.replace(/^(\s*[a-z][a-z\d+.-]*:\/\/)[^/?#]*@/i, '$1')
    credentialsHidden = display !== value
  }
  return <>{link ? <a href={display} target="_blank" rel="noreferrer">{display}</a> : display}
    {credentialsHidden && <small>地址中的登录凭证已隐藏</small>}</>
}

export function DetailList({ values, empty = '未配置' }: { values: ReactNode[]; empty?: string }) {
  return values.length ? <ul className="resource-detail-list">{values.map((value, index) => <li key={index}>{value}</li>)}</ul> : <>{empty}</>
}

export function detailDate(value?: number | string): string {
  if (value == null || value === '') return '尚未记录'
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString('zh-CN', { hour12: false }) : '未知'
}
