export type CredentialType = 'username_password' | 'access_token' | 'cookie' | 'api_key'

const ALL_CREDENTIALS: CredentialType[] = ['username_password', 'access_token', 'cookie', 'api_key']

export function credentialOptions(platform: string): CredentialType[] {
  if (normalizePlatform(platform) === 'sub2api') return ['access_token', 'api_key']
  return ALL_CREDENTIALS
}

export function normalizeCredentialType(platform: string, type: CredentialType): CredentialType {
  return credentialOptions(platform).includes(type) ? type : 'access_token'
}

export function credentialLabel(type: string, platform = ''): string {
  if (type === 'api_key') return '模型 API Key'
  if (type === 'cookie') return 'Session Cookie'
  if (type === 'access_token') return normalizePlatform(platform) === 'sub2api' ? 'Auth Token（JWT）' : '系统访问令牌'
  return '账号密码登录'
}

export function platformSupportsCheckin(platform: string): boolean {
  return normalizePlatform(platform) !== 'sub2api'
}

function normalizePlatform(platform: string): string {
  return platform.trim().toLowerCase().replaceAll('-', '')
}
