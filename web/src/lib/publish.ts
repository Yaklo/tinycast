/**
 * 生成 OBS 推流码。
 *
 * 房间名可能含中文与 `&`、`=` 这类字符，这里之所以要 **编码两次**，是被 OBS 的
 * 实际行为逼出来的：OBS 在把推流码拼进 RTMP 地址前会先做一次百分号解码（实测
 * 配置里存的是 `room=%E6%B5%8B%E8%AF%95`，线上发出的是 `room=测试`）。
 *
 * 只编码一次的话，解码后查询串里就是裸的 `测试` 甚至裸的 `&`，前者会让日志与
 * 第三方工具看不懂、后者会直接把查询串劈成两半。编码两次之后：OBS 解一次，
 * 线上仍是纯 ASCII 的单次编码，MediaMTX 原样把它交给后端，后端 `url.ParseQuery`
 * 还原成展示名；而完全不解码的客户端（ffmpeg 等）拿到双次编码，后端多解一层，
 * 两条来源最终都还原成同一个名字。
 *
 * MediaMTX 用的是干净的流名（`live/alex`），查询串只出现在它的 auth 回调里，
 * 因此这里不用担心“查询串被并进流名”这类问题。
 */
export function buildPublishCode(user: string, roomName: string, token: string): string {
  return user + '?room=' + encodeURIComponent(encodeURIComponent(roomName)) + '&token=' + token
}

/** 把服务器地址与推流码拼成一条完整推流地址，方便用户核对。 */
export function buildStreamURL(rtmpServer: string, publishCode: string): string {
  if (!rtmpServer) return publishCode
  return rtmpServer.replace(/\/+$/, '') + '/' + publishCode
}
