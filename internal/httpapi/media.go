package httpapi

import "context"

// mediaInfo 是下发给前端的推流编码信息。
//
// 前端用它做两件事：一是展示"H.264 · 1280×720"这类信息，
// 二是在 H.265 / 4:4:4 这类浏览器解不了的源上，直接给出可操作的提示，
// 而不是让用户对着黑屏猜。
type mediaInfo struct {
	VideoCodec   string `json:"videoCodec,omitempty"`
	VideoProfile string `json:"videoProfile,omitempty"`
	VideoLevel   string `json:"videoLevel,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	AudioCodec   string `json:"audioCodec,omitempty"`
}

// media 返回房间当前推流的编码信息。
//
// 未配置 SRS_STREAMS_API_URL、SRS 不可达或该流尚未出现在 streams 列表里时返回 nil，
// 前端按"未知"处理——这类信息只是提示增强，缺了不影响播放。
func (s *Server) media(ctx context.Context, username string) *mediaInfo {
	if s.streams == nil {
		return nil
	}
	stream, ok := s.streams.Stream(ctx, s.cfg.SRS.App, username)
	if !ok {
		return nil
	}
	info := &mediaInfo{
		VideoCodec:   stream.Video.Codec,
		VideoProfile: stream.Video.Profile,
		VideoLevel:   stream.Video.Level,
		Width:        stream.Video.Width,
		Height:       stream.Video.Height,
		AudioCodec:   stream.Audio.Codec,
	}
	if info.VideoCodec == "" && info.AudioCodec == "" {
		return nil
	}
	return info
}
