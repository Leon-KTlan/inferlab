package main

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

const intelligenceSuiteVersion = "canary-v1"

type IntelligenceSettings struct {
	SuiteVersion string `json:"suite_version"`
	Repetitions  int    `json:"repetitions"`
	MockProfile  string `json:"mock_profile,omitempty"`
}

type IntelligenceProbe struct {
	ID        string `json:"id"`
	Dimension string `json:"dimension"`
	Title     string `json:"title"`
	Prompt    string `json:"prompt"`
	Expected  string `json:"expected"`
}

type IntelligenceOutcome struct {
	ProbeID    string  `json:"probe_id"`
	Repetition int     `json:"repetition"`
	Passed     bool    `json:"passed"`
	LatencyMS  float64 `json:"latency_ms"`
	ErrorCode  string  `json:"error_code,omitempty"`
}

type IntelligenceDimension struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Score     float64 `json:"score"`
	Passed    int     `json:"passed"`
	Total     int     `json:"total"`
	Completed int     `json:"completed"`
}

type IntelligenceReport struct {
	SuiteVersion string                  `json:"suite_version"`
	Score        float64                 `json:"score"`
	Passed       int                     `json:"passed"`
	Total        int                     `json:"total"`
	Completed    int                     `json:"completed"`
	ErrorCount   int                     `json:"error_count"`
	Dimensions   []IntelligenceDimension `json:"dimensions"`
	Outcomes     []IntelligenceOutcome   `json:"outcomes"`
	Complete     bool                    `json:"complete"`
	Conclusion   string                  `json:"conclusion"`
}

func intelligenceDimensions() []IntelligenceDimension {
	return []IntelligenceDimension{
		{ID: "reasoning", Label: "逻辑推理"},
		{ID: "math", Label: "数学计算"},
		{ID: "instruction", Label: "指令遵循"},
		{ID: "structured", Label: "结构化输出"},
		{ID: "context", Label: "上下文检索"},
	}
}

func intelligenceProbes() []IntelligenceProbe {
	return []IntelligenceProbe{
		{ID: "reasoning-1", Dimension: "reasoning", Title: "有向先后关系", Prompt: "有三个工序A、B、C。A必须在B之前，C必须在A之前。请按从先到后的顺序，仅输出三个大写字母，不加空格、标点或解释。", Expected: "CAB"},
		{ID: "reasoning-2", Dimension: "reasoning", Title: "唯一位置推断", Prompt: "四个盒子从左到右编号1至4。红盒在蓝盒左边紧邻的位置；蓝盒是第3个；绿盒在第4个。剩下的是黄盒。黄盒编号是多少？仅输出一个阿拉伯数字。", Expected: "1"},
		{ID: "math-1", Dimension: "math", Title: "整数运算", Prompt: "计算 (17×6)−(14×3)+8。仅输出十进制整数，不加单位、标点或解释。", Expected: "68"},
		{ID: "math-2", Dimension: "math", Title: "连续折扣", Prompt: "一件商品原价240元，先降价25%，再在降价后的价格上降价10%。最终价格是多少元？仅输出十进制整数，不加单位、标点或解释。", Expected: "162"},
		{ID: "instruction-1", Dimension: "instruction", Title: "固定次序与分隔符", Prompt: "对列表 pear, apple, lime 按英文单词字母升序排列，再转为大写，用竖线|连接。仅输出结果；不得有空格、引号、代码围栏或解释。", Expected: "APPLE|LIME|PEAR"},
		{ID: "instruction-2", Dimension: "instruction", Title: "筛选与逆序", Prompt: "从序列 A1 B2 C3 D4 E5 中保留数字为偶数的项，再逆序排列，用英文逗号连接。仅输出结果；不得有空格、引号、代码围栏或解释。", Expected: "D4,B2"},
		{ID: "structured-1", Dimension: "structured", Title: "严格字段与整数", Prompt: "仅输出一个JSON对象，恰好包含两个字段：color的值为字符串teal，count的值为整数3。字段名必须小写，每个只出现一次，不得有其他字段、代码围栏或解释。", Expected: `{"color":"teal","count":3}`},
		{ID: "structured-2", Dimension: "structured", Title: "严格数组与布尔值", Prompt: "仅输出一个JSON对象，恰好包含两个字段：ids为按顺序包含字符串B2、D4的数组，ready为布尔值true。字段名必须小写，每个只出现一次，不得有其他字段、代码围栏或解释。", Expected: `{"ids":["B2","D4"],"ready":true}`},
		{ID: "context-1", Dimension: "context", Title: "按标识检索", Prompt: "给定记录：item=K7;code=BLUE。item=P4;code=AMBER。item=T9;code=GREEN。问题：item=P4的code是什么？仅输出对应的大写英文值，不加标点或解释。", Expected: "AMBER"},
		{ID: "context-2", Dimension: "context", Title: "按版本检索", Prompt: "同一设备的记录如下（版本号越大越新）：device=N2,version=1,port=4301；device=N2,version=3,port=7816；device=N2,version=2,port=5620。问题：device=N2最新版本的port是多少？仅输出四位数字，不加标点或解释。", Expected: "7816"},
	}
}

func gradeIntelligence(probeID, content string) bool {
	for _, probe := range intelligenceProbes() {
		if probe.ID != probeID {
			continue
		}
		if probe.Dimension != "structured" {
			return strings.TrimSpace(content) == probe.Expected
		}
		actual, err := intelligenceJSONValue(content)
		if err != nil {
			return false
		}
		expected, err := intelligenceJSONValue(probe.Expected)
		return err == nil && reflect.DeepEqual(actual, expected)
	}
	return false
}

// Token parsing rejects duplicate keys before JSON can silently overwrite them.
func intelligenceJSONValue(content string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	value, err := intelligenceJSONToken(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("expected one JSON value")
	}
	return value, nil
}

func intelligenceJSONToken(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("expected JSON object key")
			}
			if _, exists := object[key]; exists {
				return nil, errors.New("duplicate JSON object key")
			}
			value, err := intelligenceJSONToken(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case json.Delim('['):
		array := make([]any, 0)
		for decoder.More() {
			value, err := intelligenceJSONToken(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return array, nil
	default:
		return token, nil
	}
}

func intelligenceMockAnswer(probe IntelligenceProbe, profile string) string {
	if profile == "reference" || profile == "degraded" && (probe.Dimension == "math" || probe.Dimension == "context") {
		return probe.Expected
	}
	return "INCORRECT"
}

func summarizeIntelligence(settings IntelligenceSettings, outcomes []IntelligenceOutcome) IntelligenceReport {
	report := IntelligenceReport{
		SuiteVersion: settings.SuiteVersion,
		Dimensions:   intelligenceDimensions(),
		Outcomes:     make([]IntelligenceOutcome, 0, len(outcomes)),
	}
	dimensionIndexes := make(map[string]int, len(report.Dimensions))
	for i, dimension := range report.Dimensions {
		dimensionIndexes[dimension.ID] = i
	}
	probeDimensions := make(map[string]int)
	for _, probe := range intelligenceProbes() {
		index := dimensionIndexes[probe.Dimension]
		probeDimensions[probe.ID] = index
		report.Dimensions[index].Total += settings.Repetitions
		report.Total += settings.Repetitions
	}
	type slot struct {
		probeID    string
		repetition int
	}
	seen := make(map[slot]bool, len(outcomes))
	for _, outcome := range outcomes {
		index, known := probeDimensions[outcome.ProbeID]
		key := slot{outcome.ProbeID, outcome.Repetition}
		if !known || outcome.Repetition < 1 || outcome.Repetition > settings.Repetitions || seen[key] {
			continue
		}
		seen[key] = true
		report.Completed++
		dimension := &report.Dimensions[index]
		dimension.Completed++
		if outcome.ErrorCode != "" {
			report.ErrorCount++
			outcome.Passed = false
		}
		if outcome.Passed {
			report.Passed++
			dimension.Passed++
		}
		report.Outcomes = append(report.Outcomes, outcome)
	}
	if report.Total > 0 {
		report.Score = float64(report.Passed) / float64(report.Total) * 100
	}
	for i := range report.Dimensions {
		dimension := &report.Dimensions[i]
		if dimension.Total > 0 {
			dimension.Score = float64(dimension.Passed) / float64(dimension.Total) * 100
		}
	}
	report.Complete = report.Total > 0 && report.Completed == report.Total
	switch {
	case !report.Complete:
		report.Conclusion = "检测未完成，分数按计划题数计算，不可用于历史比较。"
	case report.ErrorCount > 0:
		report.Conclusion = "检测包含请求错误，错误题不计分，不参与历史基线比较。"
	default:
		report.Conclusion = "本分数仅表示公开合成探针的通过率，不代表人类智商、供应商身份或通用智能水平。"
	}
	return report
}
