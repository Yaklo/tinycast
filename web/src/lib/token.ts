/**
 * 生成推流令牌。
 *
 * 令牌完全在浏览器端产生（需求要求创建配置不请求后端），因此这里直接使用
 * crypto.getRandomValues 取 32 字节（256 bit）随机数，再编码成 base64url：
 * 只包含 [A-Za-z0-9_-]，既符合后端 tokenPattern，也能安全地放进 RTMP 查询串。
 */
export function generateToken(byteLength = 32): string {
  if (byteLength < 32) {
    throw new Error('推流令牌至少需要 256 bit 随机量')
  }
  const bytes = new Uint8Array(byteLength)
  crypto.getRandomValues(bytes)

  let binary = ''
  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/** 只显示令牌的一小段，用于回显"已保存的配置"，避免整串暴露在屏幕上。 */
export function maskToken(token: string): string {
  if (token.length <= 10) return '****'
  return token.slice(0, 4) + '…' + token.slice(-4)
}
