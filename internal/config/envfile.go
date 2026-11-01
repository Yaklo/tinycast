package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnvFile 从 KEY=VALUE 形式的文件读取配置，注入到进程环境变量中。
//
// 这样 Windows 上就不必逐条 set 环境变量，也不必写启动脚本：
// 一个 tinycast.env 文件就是全部配置，直接 tinycast.exe -config tinycast.env 即可。
//
// 规则：
//   - 空行与 # 开头的行被忽略，行首的 export 前缀也允许（方便和 .env / systemd 环境文件共用）；
//   - KEY=VALUE 按第一个 = 切分，值两端的空白与成对的引号会被去掉；
//   - 已经存在的环境变量不会被覆盖，命令行/系统环境永远优先于文件。
//
// 返回真正被设置的环境变量个数。
func LoadEnvFile(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("config: 打开配置文件 %s 失败: %w", path, err)
	}
	defer file.Close()

	applied := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return applied, fmt.Errorf("config: %s 第 %d 行缺少 =：%q", path, lineNo, line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return applied, fmt.Errorf("config: %s 第 %d 行的键名为空", path, lineNo)
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, unquote(strings.TrimSpace(value))); err != nil {
			return applied, fmt.Errorf("config: 设置环境变量 %s 失败: %w", key, err)
		}
		applied++
	}
	if err := scanner.Err(); err != nil {
		return applied, fmt.Errorf("config: 读取配置文件 %s 失败: %w", path, err)
	}
	return applied, nil
}

func unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
