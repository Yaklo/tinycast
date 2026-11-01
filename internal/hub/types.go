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
	// Warning 是房间的健康告警码，正常情况下为空。
	// 目前只有一种：推流码里的查询串被 SRS 当成了流名的一部分（WarnStreamUnparsed），
	// 此时观众连得上却永远收不到画面，必须提示主播重新复制推流码。
	Warning string `json:"warning,omitempty"`
}

// WarnStreamUnparsed 表示 SRS 没能把推流 URL 里的查询串与流名分开。
//
// 触发条件是推流参数里出现了未编码的非 ASCII 字符（典型是中文房间名）：
// SRS 解析 URL 失败后会退化成 name = "alex?room=测试&token=.."，
// 而播放端按干净名字 alex 请求，于是另建了一个空 source，页面永久黑屏。
const WarnStreamUnparsed = "stream_unparsed"

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
	// ReasonInvalidUsername 用户名不符合 SRS 流名规则。
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

// PublishInput 是一次 on_publish 回调的解析结果。
type PublishInput struct {
	Username string
	RoomName string
	Token    string
	// ClientID 是 SRS 侧的客户端 ID，用于识别同一条推流连接。
	ClientID string
	// IP 是推流端 IP，仅用于日志。
	IP string
	// StreamUnparsed 表示 SRS 回调里的流名仍带着查询串，
	// 说明 SRS 没能解析推流 URL（见 WarnStreamUnparsed）。
	StreamUnparsed bool
}

// PublishResult 是推流鉴权结果。
type PublishResult struct {
	Allowed bool
	Reason  Reason
	Message string
	Room    RoomView
}
