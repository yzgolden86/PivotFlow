import { useCallback, useEffect, useMemo, useState } from 'react'
import { Beaker, Fingerprint, Play, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { deleteFingerprint, getChannels, getFingerprintResults, getFingerprints, startFingerprintCalibration, startFingerprintTest, waitForFingerprintJob } from '../api'
import type { Channel, FingerprintTestRecord, ModelFingerprint } from '../types'
import { EmptyState, ErrorState, formatNumber, LoadingState, OperationNotice } from './shared'
import { Modal } from './siteShared'

type Mode = 'baselines' | 'results'
type JobMode = 'calibrate' | 'test'

export default function FingerprintsPage() {
  const [fingerprints, setFingerprints] = useState<ModelFingerprint[]>([])
  const [results, setResults] = useState<FingerprintTestRecord[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const [mode, setMode] = useState<Mode>('baselines')
  const [jobMode, setJobMode] = useState<JobMode | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true); setError('')
    try {
      const [baselineData, resultData, channelData] = await Promise.all([getFingerprints(signal), getFingerprintResults(signal), getChannels({ limit: 1000, offset: 0 }, signal)])
      setFingerprints(baselineData); setResults(resultData); setChannels(channelData.data)
    } catch (reason) { if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '模型能力基线加载失败') }
    finally { if (!signal?.aborted) setLoading(false) }
  }, [])

  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])

  const remove = async (item: ModelFingerprint) => {
    if (!window.confirm(`删除能力基线“${item.name}”？`)) return
    setBusyId(item.id); setError('')
    try { await deleteFingerprint(item.id); setFingerprints((items) => items.filter((value) => value.id !== item.id)) }
    catch (reason) { setError(reason instanceof Error ? reason.message : '删除失败') }
    finally { setBusyId(null) }
  }

  if (loading && !fingerprints.length && !results.length) return <div className="workspace-page"><LoadingState label="正在加载模型能力基线" /></div>
  if (error && !fingerprints.length && !results.length) return <div className="workspace-page"><ErrorState message={error} retry={() => void load()} /></div>

  return <div className="workspace-page fingerprints-page">
    <header className="page-header"><div><h1>模型能力基线</h1><span className="page-header-note">用固定采样记录响应耗时和结果特征，检查上游模型是否发生变化</span></div><div className="header-controls"><button className="secondary-button" type="button" onClick={() => setJobMode('test')} disabled={!fingerprints.length}><Play size={15} />开始对比</button><button className="primary-button" type="button" onClick={() => setJobMode('calibrate')}><Plus size={15} />创建基线</button><button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新模型能力基线"><RefreshCw size={17} /></button></div></header>
    {error && <OperationNotice tone="error">{error}</OperationNotice>}
    <div className="page-tabs" role="tablist"><button className={mode === 'baselines' ? 'is-active' : ''} type="button" role="tab" aria-selected={mode === 'baselines'} onClick={() => setMode('baselines')}><Fingerprint size={15} />能力基线 ({fingerprints.length})</button><button className={mode === 'results' ? 'is-active' : ''} type="button" role="tab" aria-selected={mode === 'results'} onClick={() => setMode('results')}><Beaker size={15} />对比记录 ({results.length})</button></div>

    {mode === 'baselines' ? !fingerprints.length ? <EmptyState label="还没有模型能力基线" /> : <div className="fingerprint-grid">{fingerprints.map((item) => <article className="fingerprint-card" key={item.id}>
      <header><span><Fingerprint size={18} /></span><div><strong>{item.name}</strong><small>{item.channel_name || ((item.channel_id || 0) > 0 ? `渠道 #${item.channel_id}` : '原渠道已删除')} · {item.model}</small></div><button className="icon-button danger-button" type="button" onClick={() => void remove(item)} disabled={busyId === item.id} aria-label={`删除 ${item.name}`}><Trash2 size={15} /></button></header>
      <div className="fingerprint-stats"><span><small>样本</small><strong>{item.sample_count}</strong></span><span><small>均值</small><strong>{item.stats.mean.toFixed(1)}</strong></span><span><small>波动</small><strong>{item.stats.std_dev.toFixed(1)}</strong></span><span><small>唯一值</small><strong>{item.stats.unique}</strong></span></div>
      <footer><span>{item.client_protocol}</span><span>{item.prompt_version}</span></footer>
    </article>)}</div> : !results.length ? <EmptyState label="还没有模型指纹对比记录" /> : <div className="records-panel"><div className="record-head fingerprint-result-grid"><span>时间</span><span>渠道 / 模型</span><span>样本</span><span>最佳匹配</span></div>{results.map((item) => <article className="record-row fingerprint-result-grid" key={item.id}><div><strong>{formatDate(item.created_at)}</strong><span>#{item.id}</span></div><div><strong>{item.channel_name}</strong><span>{item.model}</span></div><div><strong>{formatNumber(item.sample_count)}</strong><span>采样次数</span></div><div><strong>{(item.best_score * 100).toFixed(1)}%</strong><span>{matchLabel(item.matches)}</span></div></article>)}</div>}
    {jobMode && <FingerprintJobForm mode={jobMode} channels={channels} fingerprints={fingerprints} close={() => setJobMode(null)} finished={() => { setJobMode(null); void load() }} />}
  </div>
}

function FingerprintJobForm({ mode, channels, fingerprints, close, finished }: { mode: JobMode; channels: Channel[]; fingerprints: ModelFingerprint[]; close: () => void; finished: () => void }) {
  const firstChannel = channels.find((channel) => channel.enabled) || channels[0]
  const [channelId, setChannelId] = useState(firstChannel?.id || 0)
  const channel = channels.find((item) => item.id === channelId)
  const models = useMemo(() => channel?.models.filter((item) => !item.disabled) || [], [channel])
  const [model, setModel] = useState(models[0]?.model || '')
  const [name, setName] = useState('')
  const [baselineId, setBaselineId] = useState(fingerprints[0]?.id || 0)
  const [protocol, setProtocol] = useState('anthropic')
  const [iterations, setIterations] = useState(10)
  const [concurrency, setConcurrency] = useState(2)
  const [stream, setStream] = useState(true)
  const [running, setRunning] = useState(false)
  const [progress, setProgress] = useState('')
  const [error, setError] = useState('')

  useEffect(() => { if (!models.some((item) => item.model === model)) setModel(models[0]?.model || '') }, [model, models])

  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setRunning(true); setError(''); setProgress('正在创建任务')
    try {
      const common = { channel_id: channelId, model, client_protocol: protocol, iterations, concurrency, key_index: 0, stream }
      const queued = mode === 'calibrate' ? await startFingerprintCalibration({ ...common, name: name.trim() }) : await startFingerprintTest({ ...common, fingerprint_id: baselineId || undefined })
      setProgress('正在采样并计算分布')
      const job = await waitForFingerprintJob(queued.job_id)
      if (job.status !== 'succeeded') throw new Error(job.error || `任务${job.status}`)
      finished()
    } catch (reason) { setError(reason instanceof Error ? reason.message : '指纹任务失败'); setProgress('') }
    finally { setRunning(false) }
  }

  return <Modal title={mode === 'calibrate' ? '创建能力基线' : '开始能力对比'} close={close} wide><form className="console-form" onSubmit={submit}>
    {error && <div className="modal-error inline-error">{error}</div>}
    {mode === 'calibrate' && <label>基线名称<input required value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：官方 Claude Sonnet" /></label>}
    {mode === 'test' && <label>对比基线<select value={baselineId} onChange={(event) => setBaselineId(Number(event.target.value))}>{fingerprints.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}</select></label>}
    <div className="form-grid"><label>渠道<select value={channelId} onChange={(event) => setChannelId(Number(event.target.value))}>{channels.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}</select></label><label>模型<select required value={model} onChange={(event) => setModel(event.target.value)}>{models.map((item) => <option value={item.model} key={item.model}>{item.model}</option>)}</select></label><label>客户端协议<select value={protocol} onChange={(event) => setProtocol(event.target.value)}><option value="anthropic">Anthropic</option><option value="openai">OpenAI Chat</option><option value="codex">OpenAI Responses</option><option value="gemini">Gemini</option></select></label><label>采样次数<input type="number" min="3" max="100" value={iterations} onChange={(event) => setIterations(Number(event.target.value))} /></label><label>并发数<input type="number" min="1" max="10" value={concurrency} onChange={(event) => setConcurrency(Number(event.target.value))} /></label><label className="checkbox-field"><input type="checkbox" checked={stream} onChange={(event) => setStream(event.target.checked)} />流式请求</label></div>
    {progress && <OperationNotice persistent><RefreshCw className="spin" size={15} />{progress}</OperationNotice>}
    <footer><button className="secondary-button" type="button" onClick={close} disabled={running}>取消</button><button className="primary-button" type="submit" disabled={running || !channelId || !model || (mode === 'calibrate' && !name.trim())}>{running ? <RefreshCw className="spin" size={15} /> : <Play size={15} />}{running ? '运行中' : '开始'}</button></footer>
  </form></Modal>
}

function formatDate(value: string): string { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date) }
function matchLabel(matches?: unknown[]): string { if (!matches?.length) return '无匹配详情'; const first = matches[0] as { name?: string; fingerprint_name?: string }; return first.name || first.fingerprint_name || '最高相似度' }
