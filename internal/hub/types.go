package hub

// State 表示房间生命周期状态。
//
// LIVE：正常推流中；GRACE：推流断开，保留 grace 时长等待原身份重连；
// RELEASED 是终态，房间与身份绑定被彻底删除，因此不会再出现在任何视图中。
type State string

const (
	StateLive  State = "LIVE"
	StateGrace State = "GRACE"
)

// MediaInfo 是推流的编码参数，直接来自 MediaMTX 的推流清单。
//
// 前端用它判断浏览器能不能解码（例如 H.265 在 WebRTC 里必然黑屏，
// 4:4:4 / 10bit 在多数浏览器上也不受支持），从而给出可操作的提示而不是静默黑屏。
type MediaInfo struct {
	// VideoCodec 取 MediaMTX 的编码名：H264 / H265 / AV1 / VP8 / VP9 ...
	VideoCodec string `json:"videoCodec,omitempty"`
	// VideoProfile / VideoLevel 是 H.264 的 profile 与 level（其它编码为空）。
	VideoProfile string `json:"videoProfile,omitempty"`
	VideoLevel   string `json:"videoLevel,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	// AudioCodec 取 MediaMTX 的编码名：Opus / MPEG-4 Audio / G711 ...
	AudioCodec string `json:"audioCodec,omitempty"`
}

// RoomView 是房间对外暴露的只读视图，不包含任何推流令牌信息（连哈希都不暴露）。
type RoomView struct {
	Username    string `json:"username"`
	Room        string `json:"room"`
	State       State  `json:"state"`
	ViewerCount int    `json:"viewerCount"`
	// StartedAt 是本轮 LIVE 开始时间（Unix 毫秒）。
	StartedAt int64 `json:"startedAt"`
	// GraceUntil 是 GRACE 状态的释放时间点（Unix 毫秒），LIVE 时为 0。
	GraceUntil int64 `json:"graceUntil"`
	// Media 是本轮推流的编码参数，尚未探测到时为 nil。
	Media *MediaInfo `json:"media,omitempty"`
}

// Snapshot 是一次完整的状态快照，通过 SSE 推送给前端。
//
// 前端只依赖快照（而非增量补丁），因此丢失任意一帧都不会造成状态错乱。
type Snapshot struct {
	Seq        uint64     `json:"seq"`
	ServerTime int64      `json:"serverTime"`
	Rooms      []RoomView `json:"rooms"`
}

// Reason 是推流鉴权失败的原因码，同时用于日志与调试。
type Reason string

const (
	// ReasonInvalidUsername 用户名不符合流名规则。
	ReasonInvalidUsername Reason = "invalid_username"
	// ReasonInvalidToken 推流令牌格式非法。
	ReasonInvalidToken Reason = "invalid_token"
	// ReasonInvalidRoom 房间名称为空或过长。
	ReasonInvalidRoom Reason = "invalid_room"
	// ReasonUsernameTokenConflict 用户名已绑定其他令牌（规则 1）。
	ReasonUsernameTokenConflict Reason = "username_bound_to_other_token"
	// ReasonTokenUsernameConflict 令牌已绑定其他用户名（规则 2）。
	ReasonTokenUsernameConflict Reason = "token_bound_to_other_username"
	// ReasonAlreadyPublishing 同一身份已有推流连接（规则 3）。
	ReasonAlreadyPublishing Reason = "already_publishing"
)

// BindInput 是一次推流鉴权请求的解析结果。
type BindInput struct {
	Username string
	RoomName string
	Token    string
	// SessionID 是 MediaMTX 的会话 ID（鉴权回调里的 id 字段），用于识别同一条推流连接。
	SessionID string
	// IP 是推流端 IP，仅用于日志。
	IP string
}

// BindResult 是推流鉴权结果。
type BindResult struct {
	Allowed bool
	Reason  Reason
	Message string
	Room    RoomView
}
