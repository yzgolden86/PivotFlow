import type { Channel, Site, SiteAccount, SiteAccountModel } from '../types.ts'
import { eligibleAccountTestTargets, eligibleChannelTestTargets, eligibleModelNames } from './modelTestEligibility.ts'

export type ConnectConfigurationInput = {
  sites: readonly Site[] | null
  accounts: readonly SiteAccount[] | null
  facts: readonly SiteAccountModel[] | null
  channels: readonly Channel[] | null
}

export type ConnectConfigurationSummary = {
  usableAccounts: number
  usableChannels: number
  usableSites: number
  modelCount: number
  unknownSources: number
  partial: boolean
}

export type ConnectGuideStepState = 'ready' | 'pending' | 'unknown'

export type ConnectGuideStep = {
  id: 'source' | 'model' | 'channel' | 'client' | 'verification'
  label: string
  ready: boolean
  state: ConnectGuideStepState
  detail: string
  links: ReadonlyArray<readonly [label: string, href: string]>
  scope: 'configuration' | 'verification'
}

function boundedCount(value: number): string {
  return value > 99 ? '99+' : String(value)
}

function configurationState(ready: boolean, unknown: boolean): ConnectGuideStepState {
  if (ready) return 'ready'
  return unknown ? 'unknown' : 'pending'
}

export function buildConnectionGuide({ configuration, tokens, configured, verified, loading }: {
  configuration: ConnectConfigurationInput
  tokens: number
  configured: boolean
  verified: boolean
  loading: boolean
}) {
  const { sites, accounts, facts, channels } = configuration
  const accountTargets = sites && accounts && facts ? eligibleAccountTestTargets(accounts, sites, facts) : []
  const channelTargets = channels ? eligibleChannelTestTargets(channels.filter((channel) => channel.enabled)) : []
  const accountByID = new Map((accounts || []).map((account) => [account.id, account]))
  const usableSiteIDs = new Set(accountTargets.flatMap((target) => {
    const account = accountByID.get(target.id)
    return account ? [account.site_id] : []
  }))
  const modelCount = eligibleModelNames(accountTargets, channelTargets).size
  const unknownSources = [sites, accounts, facts, channels].filter((source) => source === null).length
  const partial = unknownSources > 0
  const usableAccounts = accountTargets.length
  const usableChannels = channelTargets.length
  const sourceReady = usableAccounts > 0 || usableChannels > 0
  const modelReady = modelCount > 0
  const channelReady = usableChannels > 0
  const sourceUnknown = !sourceReady && (sites === null || accounts === null || facts === null || channels === null)
  const modelUnknown = !modelReady && (sites === null || accounts === null || facts === null || channels === null)
  const channelUnknown = !channelReady && channels === null
  const configurationSummary: ConnectConfigurationSummary = {
    usableAccounts,
    usableChannels,
    usableSites: usableSiteIDs.size,
    modelCount,
    unknownSources,
    partial,
  }
  const steps: ConnectGuideStep[] = [
    {
      id: 'source',
      label: '来源配置',
      ready: sourceReady,
      state: configurationState(sourceReady, sourceUnknown),
      detail: sourceReady ? `可用账号 ${boundedCount(usableAccounts)} 个 / 可用渠道 ${boundedCount(usableChannels)} 个` : sourceUnknown ? '相关读取失败，未知来源未计入' : '需要启用站点账号下的当前模型事实，或配置可路由渠道',
      links: [['站点与账号', '#/sites?onboarding=1'], ['API Key / OAuth', '#/channels?onboarding=1']],
      scope: 'configuration',
    },
    {
      id: 'model',
      label: '模型配置',
      ready: modelReady,
      state: configurationState(modelReady, modelUnknown),
      detail: modelReady ? `已知可用模型 ${boundedCount(modelCount)} 个` : modelUnknown ? '模型事实或渠道读取失败，未知模型未计入' : '需要非停用、非过期的模型事实，或启用渠道模型',
      links: [['刷新账号模型', '#/accounts?onboarding=1'], ['模型测试', '#/models?onboarding=1']],
      scope: 'configuration',
    },
    {
      id: 'channel',
      label: '渠道配置',
      ready: channelReady,
      state: configurationState(channelReady, channelUnknown),
      detail: channelReady ? `已启用且具备模型、URL 与凭证来源的渠道 ${boundedCount(usableChannels)} 个` : channelUnknown ? '渠道读取失败，未知渠道未计入' : '启用至少一个具备有效模型、URL 和凭证来源的渠道',
      links: [['检查与同步渠道', '#/channels?onboarding=1']],
      scope: 'configuration',
    },
    {
      id: 'client',
      label: '生成客户端配置',
      ready: configured,
      state: configured ? 'ready' : 'pending',
      detail: configured ? '当前客户端配置模板可生成' : tokens ? '读取令牌并选择可用模型' : '先创建或选择下游令牌',
      links: [],
      scope: 'configuration',
    },
  ]
  const verification: ConnectGuideStep = {
    id: 'verification',
    label: '验证首个请求',
    ready: verified,
    state: verified ? 'ready' : 'pending',
    detail: verified ? '本次同源网关调用已通过' : '在验证区单独执行，并按正常调用计费',
    links: [],
    scope: 'verification',
  }
  steps.push(verification)
  const prerequisites = steps.slice(0, 3)
  const readyPrerequisites = prerequisites.filter((step) => step.ready).length
  const knownCounts = `${boundedCount(usableAccounts)} 个可用账号 / ${boundedCount(usableChannels)} 个可用渠道`
  const summary = loading
    ? '正在读取配置条件'
    : partial
      ? `配置条件读取不完整 · 已确认 ${knownCounts} · 未知项未计入`
      : `配置条件 · ${knownCounts} · ${boundedCount(modelCount)} 个已知模型`

  return {
    steps,
    prerequisites,
    verification,
    configuration: configurationSummary,
    configurationReadyCount: readyPrerequisites,
    prerequisitesReady: readyPrerequisites === prerequisites.length,
    summary,
  }
}
