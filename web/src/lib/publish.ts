/**
 * 生成 OBS 推流码。
 *
 * 房间名可能含中文，这里之所以要 **编码两次**，是被 OBS 的实际行为逼出来的：
 * OBS 在把推流码拼进 RTMP 地址前会先做一次百分号解码（实测 OBS 配置里存的是
 * `room=%E6%B5%8B%E8%AF%95`，SRS 收到的却是 `room=测试`）。而 SRS 一旦在查询串里
 * 看到未编码的非 ASCII 字符，就会解析失败（code=3007 HttpParseUrl），把整段查询串
 * 并进流名变成 `alex?room=测试&token=..`，播放端按干净名字 `alex` 请求永远拿不到画面，
 * 浏览器侧表现为黑屏且没有任何报错。
 *
 * 编码两次之后：OBS 解一次，线上仍是纯 ASCII 的单次编码（SRS 能正常解析）；
 * ffmpeg / tinysim 这类完全不解码的客户端拿到的是双次编码，后端会多解一层，
 * 两条来源最终都还原成同一个展示名。
 */
export function buildPublishCode(user: string, roomName: string, token: string): string {
  return user + '?room=' + encodeURIComponent(encodeURIComponent(roomName)) + '&token=' + token
}

/** 把服务器地址与推流码拼成一条完整推流地址，方便用户核对。 */
export function buildStreamURL(rtmpServer: string, publishCode: string): string {
  if (!rtmpServer) return publishCode
  return rtmpServer.replace(/\/+$/, '') + '/' + publishCode
}
