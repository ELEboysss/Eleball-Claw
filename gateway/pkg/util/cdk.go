package util

import (
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
)

const cdkAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// GenerateCDKCode 生成指定长度的随机兑换码
func GenerateCDKCode(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("兑换码长度必须大于 0")
	}
	max := big.NewInt(int64(len(cdkAlphabet)))
	var sb strings.Builder
	sb.Grow(length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteByte(cdkAlphabet[n.Int64()])
	}
	return sb.String(), nil
}

// NormalizeCDKCode 标准化用户输入的兑换码：去横杠、去空格、转大写
func NormalizeCDKCode(code string) string {
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	code = strings.ToUpper(code)
	return code
}

// FormatCDKCode 把无横杠兑换码格式化为 XXXX-XXXX-XXXX-XXXX 展示
func FormatCDKCode(code string) string {
	if len(code) <= 4 {
		return code
	}
	var parts []string
	for i := 0; i < len(code); i += 4 {
		end := i + 4
		if end > len(code) {
			end = len(code)
		}
		parts = append(parts, code[i:end])
	}
	return strings.Join(parts, "-")
}
