// Command tinysim 用 HTTP 请求模拟 SRS 的四个业务回调。
//
// 它不属于服务端运行时：只是为了让没有 SRS 的机器（例如 Windows 测试机）
// 也能亲手走通"推流鉴权 → 建房 → 观看计数 → 断流保留 → 重新推流"的完整链路。
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误："+err.Error())
		os.Exit(1)
	}
}

// sim 保存一次联调所需的参数。
type sim struct {
	base    string
	app     string
	user    string
	room    string
	token   string
	secret  string
	client  string
	step    time.Duration
	viewers int
	http    *http.Client
}

// reply 是一次回调的原始响应。
type reply struct {
	status int
	body   string
	reason string
}

// roomState 是房间详情接口里我们关心的字段。
type roomState struct {
	Username    string `json:"username"`
	Room        string `json:"room"`
	State       string `json:"state"`
	ViewerCount int    `json:"viewerCount"`
	GraceUntil  int64  `json:"graceUntil"`
}

func run() error {
	command := "help"
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		command = os.Args[1]
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}

	s := &sim{}
	fs := flag.NewFlagSet("tinysim", flag.ExitOnError)
	fs.StringVar(&s.base, "base", "http://127.0.0.1:8080", "tinycast 服务地址")
	fs.StringVar(&s.app, "app", "live", "SRS 应用名")
	fs.StringVar(&s.user, "user", "alex", "推流用户名，同时是 SRS 流名")
	fs.StringVar(&s.room, "room", "演示直播间", "房间展示名称，仅用于展示")
	fs.StringVar(&s.token, "token", "", "推流令牌；留空则自动生成")
	fs.StringVar(&s.secret, "secret", "", "回调共享密钥，需与服务端 SRS_CALLBACK_SECRET 一致")
	fs.StringVar(&s.client, "client", "", "客户端 ID；留空则自动生成")
	fs.DurationVar(&s.step, "step", 4*time.Second, "demo 场景中每一步的停留时间")
	fs.IntVar(&s.viewers, "viewers", 3, "demo 场景中模拟的观众数量")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	// 允许把子命令写在参数之后（如 -base ... demo），flag.Parse 会在第一个非 flag 处停下。
	if command == "help" && fs.NArg() > 0 {
		command = fs.Arg(0)
	}

	s.base = strings.TrimRight(s.base, "/")
	s.http = &http.Client{Timeout: 10 * time.Second}
	if s.token == "" {
		s.token = generateToken()
	}

	switch command {
	case "demo":
		return s.demo()
	case "publish":
		return s.do("on_publish", s.clientOr("sim-pub-1"))
	case "unpublish":
		return s.do("on_unpublish", s.clientOr("sim-pub-1"))
	case "play":
		return s.do("on_play", s.clientOr("sim-view-1"))
	case "stop":
		return s.do("on_stop", s.clientOr("sim-view-1"))
	case "state":
		return s.printState("当前状态")
	case "token":
		s.printConfig()
		return nil
	case "help", "-h", "--help":
		s.printConfig()
		s.usage()
		return nil
	default:
		s.usage()
		return fmt.Errorf("未知子命令 %q", command)
	}
}

func (s *sim) clientOr(def string) string {
	if s.client != "" {
		return s.client
	}
	return def
}

func (s *sim) usage() {
	fmt.Print("子命令：\n" +
		"  demo        完整走一遍：推流 → 重复推流被拒 → 观众进出 → 断流保留 → 重新推流\n" +
		"  publish     只发 on_publish（模拟 OBS 开始推流）\n" +
		"  unpublish   只发 on_unpublish（模拟 OBS 停止推流）\n" +
		"  play        只发 on_play（模拟一个观众进入）\n" +
		"  stop        只发 on_stop（模拟一个观众离开）\n" +
		"  state       查询房间当前状态\n" +
		"  token       打印一份可直接粘贴到 OBS 的推流配置\n" +
		"\n常用参数：-user alex -room 演示直播间 -token <令牌> -base http://127.0.0.1:8080\n")
}

func (s *sim) printConfig() {
	room := url.QueryEscape(s.room)
	base := rtmpBase(s.base)
	fmt.Print("推流配置（可直接填入 OBS）：\n" +
		"  服务器地址：" + base + "\n" +
		"  推流码：    " + s.user + "?room=" + room + "&token=" + s.token + "\n" +
		"  完整地址：  " + base + "/" + s.user + "?room=" + room + "&token=" + s.token + "\n" +
		"  观看页面：  " + s.base + "/r/" + url.PathEscape(s.user) + "\n\n")
}

// rtmpBase 返回展示用的 RTMP 地址；本地联调时 RTMP 端口按 SRS 默认值展示。
func rtmpBase(base string) string {
	host := "localhost"
	if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return "rtmp://" + host + ":1935/live"
}

func (s *sim) param() string {
	q := url.Values{}
	q.Set("room", s.room)
	q.Set("token", s.token)
	return q.Encode()
}

// callback 发送一次回调请求，返回原始响应。
func (s *sim) callback(action, clientID string) (reply, error) {
	payload := map[string]string{
		"action":    action,
		"client_id": clientID,
		"ip":        "127.0.0.1",
		"vhost":     "__defaultVhost__",
		"app":       s.app,
		"stream":    s.user,
		"param":     "?" + s.param(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return reply{}, err
	}

	target := s.base + "/internal/srs/" + action
	if s.secret != "" {
		target += "?secret=" + url.QueryEscape(s.secret)
	}
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return reply{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return reply{}, fmt.Errorf("请求 %s 失败：%w（tinycast 是否已启动？）", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return reply{
		status: resp.StatusCode,
		body:   strings.TrimSpace(string(raw)),
		reason: resp.Header.Get("X-Tinycast-Reason"),
	}, nil
}

// do 发送回调并要求被放行。
func (s *sim) do(action, clientID string) error {
	r, err := s.callback(action, clientID)
	if err != nil {
		return err
	}
	if r.status != http.StatusOK || r.body != "0" {
		return fmt.Errorf("%s 被拒绝：HTTP %d，原因 %s（响应 %q）", action, r.status, orDefault(r.reason, "未知"), r.body)
	}
	fmt.Printf("  ✓ %-12s client=%s → 放行\n", action, clientID)
	return nil
}

func (s *sim) stateOf() (*roomState, error) {
	resp, err := s.http.Get(s.base + "/api/rooms/" + url.PathEscape(s.user))
	if err != nil {
		return nil, fmt.Errorf("查询房间失败：%w（tinycast 是否已启动？）", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("查询房间返回 HTTP %d", resp.StatusCode)
	}
	var wrapper struct {
		Room roomState `json:"room"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wrapper); err != nil {
		return nil, err
	}
	return &wrapper.Room, nil
}

func (s *sim) printState(label string) error {
	view, err := s.stateOf()
	if err != nil {
		return err
	}
	switch {
	case view == nil:
		fmt.Printf("  · %s：房间不存在（已释放）\n", label)
	case view.State == "GRACE":
		remain := time.Until(time.UnixMilli(view.GraceUntil)).Round(time.Second)
		fmt.Printf("  · %s：%s 观众 %d 人，断流保留剩余 %s\n", label, view.State, view.ViewerCount, remain)
	default:
		fmt.Printf("  · %s：%s 观众 %d 人\n", label, view.State, view.ViewerCount)
	}
	return nil
}

// demo 按需求里的生命周期顺序跑一遍，每一步都回读服务端状态。
func (s *sim) demo() error {
	fmt.Println("== tinycast 联调演示：模拟 SRS 回调走一遍房间生命周期 ==")
	s.printConfig()
	s.pause("请先打开上面的观看页面，下面开始推流")

	fmt.Println("[1/6] 模拟 OBS 开始推流")
	if err := s.do("on_publish", "sim-pub-1"); err != nil {
		return err
	}
	if err := s.printState("推流后"); err != nil {
		return err
	}

	fmt.Println("[2/6] 同一身份并发推流应当被拒绝")
	r, err := s.callback("on_publish", "sim-pub-dup")
	if err != nil {
		return err
	}
	if r.status == http.StatusOK && r.body == "0" {
		fmt.Println("  ✗ 预期被拒绝，但服务端放行了第二条连接")
	} else {
		fmt.Printf("  ✓ 已拒绝（原因 %s）\n", orDefault(r.reason, "未知"))
	}

	fmt.Println("[3/6] 模拟观众进入")
	for i := 1; i <= s.viewers; i++ {
		if err := s.do("on_play", fmt.Sprintf("sim-view-%d", i)); err != nil {
			return err
		}
	}
	if err := s.printState("观众进入后"); err != nil {
		return err
	}

	if s.viewers >= 2 {
		fmt.Println("[4/6] 一名观众离开")
		if err := s.do("on_stop", "sim-view-2"); err != nil {
			return err
		}
		if err := s.printState("观众离开后"); err != nil {
			return err
		}
	}

	fmt.Println("[5/6] 模拟 OBS 停止推流，房间进入保留期")
	if err := s.do("on_unpublish", "sim-pub-1"); err != nil {
		return err
	}
	if err := s.printState("断流后"); err != nil {
		return err
	}
	s.pause("首页此时应显示「断流保留」并开始倒计时")

	fmt.Println("[6/6] 保留期内用同一身份重新推流")
	if err := s.do("on_publish", "sim-pub-2"); err != nil {
		return err
	}
	if err := s.printState("重新推流后"); err != nil {
		return err
	}
	if err := s.do("on_unpublish", "sim-pub-2"); err != nil {
		return err
	}
	if err := s.printState("再次断流后"); err != nil {
		return err
	}

	fmt.Println("演示结束。保留期结束后后台清理任务会释放该房间；把服务端 ROOM_GRACE 设为 30s 可以快速看到释放。")
	return nil
}

func (s *sim) pause(msg string) {
	if s.step <= 0 {
		return
	}
	fmt.Printf("  … %s（停留 %s）\n", msg, s.step)
	time.Sleep(s.step)
}

func generateToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(errors.New("无法获取随机数：" + err.Error()))
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
