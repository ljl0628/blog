package service

import (
	"reflect"
	"strings"
	"testing"

	"blog/server/internal/model"
)

// TestRagEvalHit 命中判定纯函数：交集即命中、无交集/空集不命中、重复 ID 不影响。
func TestRagEvalHit(t *testing.T) {
	tests := []struct {
		name      string
		expectIDs []uint
		actualIDs []uint
		want      bool
	}{
		{"期望文章在 top-k 首位", []uint{1}, []uint{1, 2, 3}, true},
		{"期望文章在 top-k 尾部", []uint{3}, []uint{1, 2, 3}, true},
		{"多个期望命中其一即命中", []uint{7, 3}, []uint{1, 2, 3}, true},
		{"无交集不命中", []uint{9}, []uint{1, 2, 3}, false},
		{"实际集为空不命中", []uint{1}, nil, false},
		{"期望集为空不命中", nil, []uint{1, 2}, false},
		{"双方为空不命中", nil, nil, false},
		{"重复 ID 不影响判定", []uint{1, 1}, []uint{1, 1, 2}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evalHit(tt.expectIDs, tt.actualIDs); got != tt.want {
				t.Fatalf("evalHit(%v, %v) = %v, want %v", tt.expectIDs, tt.actualIDs, got, tt.want)
			}
		})
	}
}

// TestRagEvalCitationOK 引用正确性判定纯函数：非空且与期望集有交集才通过，
// 无引用 / 引用全在期望集外不通过；夹带无关文章与未解析引用（0 占位）不因此判负。
func TestRagEvalCitationOK(t *testing.T) {
	tests := []struct {
		name        string
		expectIDs   []uint
		citationIDs []uint
		want        bool
	}{
		{"引用恰为期望集", []uint{1, 2}, []uint{1, 2}, true},
		{"引用是期望集真子集", []uint{1, 2, 3}, []uint{2}, true},
		{"重复引用同一期望文章", []uint{1, 2}, []uint{1, 1, 2}, true},
		{"引用夹带无关文章但命中其一仍通过", []uint{1, 2}, []uint{1, 9}, true},
		{"引用全在期望集外", []uint{1}, []uint{8, 9}, false},
		{"无引用不通过", []uint{1}, nil, false},
		{"期望集为空任何引用都不通过", nil, []uint{1}, false},
		{"引用全为未解析占位(0)不通过", []uint{1, 2}, []uint{0, 0}, false},
		{"未解析占位不影响已命中引用", []uint{1, 2}, []uint{1, 0}, true},
		{"双方为空不通过", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evalCitationOK(tt.expectIDs, tt.citationIDs); got != tt.want {
				t.Fatalf("evalCitationOK(%v, %v) = %v, want %v", tt.expectIDs, tt.citationIDs, got, tt.want)
			}
		})
	}
}

// TestRagEvalParseExpectIDs 期望文章集解析：合法数组、空载荷/空数组、非法 JSON。
func TestRagEvalParseExpectIDs(t *testing.T) {
	tests := []struct {
		name    string
		raw     model.JSON
		want    []uint
		wantErr string // 期望错误信息含该子串；空串表示应无错
	}{
		{"正常数组", model.JSON(`[1,2,3]`), []uint{1, 2, 3}, ""},
		{"单元素", model.JSON(`[42]`), []uint{42}, ""},
		{"空数组报错", model.JSON(`[]`), nil, "为空"},
		{"空载荷报错", model.JSON(``), nil, "为空"},
		{"非数组 JSON 报错", model.JSON(`{"a":1}`), nil, "解析"},
		{"元素非整数报错", model.JSON(`["a"]`), nil, "解析"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCaseExpectIDs(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("parseCaseExpectIDs(%s) 不应报错, got %v", tt.raw, err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("parseCaseExpectIDs(%s) = %v, want %v", tt.raw, got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseCaseExpectIDs(%s) 应报错含 %q, 实得 nil", tt.raw, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("parseCaseExpectIDs(%s) 错误 %q 应含子串 %q", tt.raw, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestRagEvalParseCitationIdx(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   []int
	}{
		{"常规标注", "Pydantic 用于消息建模 [1]，传输层由 httpx 承担 [2]，再回看 [1]。",
			[]int{1, 2}},
		{"连续标注", "两篇都涉及 [1][2]，结论一致。", []int{1, 2}},
		{"行首标注", "[1] 开头就引用。", []int{1}},
		{"代码下标不误判", "取首元素 a[1] 与末元素 data[3]。", []int{}},
		{"数组字面量不误判", "初始化为 [1, 2, 3] 后使用。", []int{}},
		{"无标注", "通篇没有任何引用。", []int{}},
		{"幻觉大序号仍提取", "越界与否由调用方判 [99]。", []int{99}},
		{"三位数不匹配", "[123] 超出两位上限。", []int{}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAnswerCitationIdx(tt.answer)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseAnswerCitationIdx(%q) = %v, want %v", tt.answer, got, tt.want)
			}
		})
	}
}
