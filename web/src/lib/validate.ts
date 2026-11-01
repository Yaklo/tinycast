/** 与后端 hub.usernamePattern 保持一致。 */
export const USERNAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{2,31}$/

/** 与后端 hub.MaxRoomNameRunes 保持一致。 */
export const MAX_ROOM_NAME_RUNES = 40

/** 校验用户名，返回错误提示；合法时返回空串。 */
export function validateUsername(raw: string): string {
  const value = raw.trim()
  if (!value) return '请填写用户名'
  if (!USERNAME_PATTERN.test(value)) {
    return '用户名需为 3-32 位，只能用字母、数字、下划线和短横线，且以字母或数字开头'
  }
  return ''
}

/** 校验房间名称，返回错误提示；合法时返回空串。 */
export function validateRoomName(raw: string): string {
  const value = raw.trim()
  if (!value) return '请填写房间名称'
  if ([...value].length > MAX_ROOM_NAME_RUNES) return '房间名称不能超过' + MAX_ROOM_NAME_RUNES + '个字符'
  if (/[\u0000-\u001f\u007f]/.test(value)) return '房间名称不能包含控制字符'
  return ''
}
