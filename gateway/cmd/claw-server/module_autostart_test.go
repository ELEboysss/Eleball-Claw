package main

import "testing"

// moduleImageRef 镜像引用拼接：<registry>/<module>:<tag>
func TestModuleImageRef(t *testing.T) {
	cases := []struct {
		name     string
		registry string
		module   string
		tag      string
		want     string
	}{
		{
			name:     "默认 ACR 前缀",
			registry: "crpi-2tmk9w177nykk4zb.cn-hangzhou.personal.cr.aliyuncs.com/eleball",
			module:   "search-web",
			tag:      "develop",
			want:     "crpi-2tmk9w177nykk4zb.cn-hangzhou.personal.cr.aliyuncs.com/eleball/search-web:develop",
		},
		{
			name:     "registry 末尾斜杠去除",
			registry: "registry.example.com/eleball/",
			module:   "stt",
			tag:      "develop",
			want:     "registry.example.com/eleball/stt:develop",
		},
		{
			name:     "自定义 tag",
			registry: "registry.example.com/eleball",
			module:   "firecrawl",
			tag:      "v1.2.3",
			want:     "registry.example.com/eleball/firecrawl:v1.2.3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := moduleImageRef(tc.registry, tc.module, tc.tag); got != tc.want {
				t.Fatalf("moduleImageRef(%q, %q, %q) = %q, want %q", tc.registry, tc.module, tc.tag, got, tc.want)
			}
		})
	}
}

// normalizePullPolicy 策略归一化：合法值原样返回，空/非法值回退 pull_first
func TestNormalizePullPolicy(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{pullPolicyPullFirst, pullPolicyPullFirst},
		{pullPolicyBuildOnly, pullPolicyBuildOnly},
		{pullPolicyPullOnly, pullPolicyPullOnly},
		{"", pullPolicyPullFirst},
		{"PULL_FIRST", pullPolicyPullFirst}, // 大小写不敏感暂不放宽：非法值回退默认
		{"garbage", pullPolicyPullFirst},
	}
	for _, tc := range cases {
		if got := normalizePullPolicy(tc.in); got != tc.want {
			t.Fatalf("normalizePullPolicy(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
