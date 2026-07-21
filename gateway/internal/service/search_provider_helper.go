package service

import (
	"os"
	"regexp"
	"strings"
)

// getEnvDefault 获取环境变量，未设置时返回默认值
func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// stripHTMLTags 去除 HTML 标签
func stripHTMLTags(input string) string {
	re := regexp.MustCompile("<[^>]*>")
	return strings.TrimSpace(re.ReplaceAllString(input, ""))
}
