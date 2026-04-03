package translate

import (
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Templatize 单元测试
// ─────────────────────────────────────────────────────────────────────────────

func TestTemplatize_BacktickIdentifiers(t *testing.T) {
	input := "variable `foo` declared but not used"
	result := Templatize(input)

	if !result.IsTemplated {
		t.Fatal("期望 IsTemplated=true")
	}
	if len(result.Identifiers) != 1 {
		t.Fatalf("期望 1 个标识符，实际 %d 个", len(result.Identifiers))
	}
	if result.Identifiers[0] != "`foo`" {
		t.Errorf("期望标识符 `foo`，实际 %q", result.Identifiers[0])
	}
	if result.Template != "variable $ID_0$ declared but not used" {
		t.Errorf("模板不匹配: %q", result.Template)
	}
}

func TestTemplatize_SingleQuoteIdentifiers(t *testing.T) {
	input := "variable 'bar' declared but not used"
	result := Templatize(input)

	if !result.IsTemplated {
		t.Fatal("期望 IsTemplated=true")
	}
	if result.Identifiers[0] != "'bar'" {
		t.Errorf("期望标识符 'bar'，实际 %q", result.Identifiers[0])
	}
}

func TestTemplatize_DoubleQuoteIdentifiers(t *testing.T) {
	input := `cannot find module "github.com/foo/bar"`
	result := Templatize(input)

	if !result.IsTemplated {
		t.Fatal("期望 IsTemplated=true")
	}
	if result.Identifiers[0] != `"github.com/foo/bar"` {
		t.Errorf("期望标识符 \"github.com/foo/bar\"，实际 %q", result.Identifiers[0])
	}
}

func TestTemplatize_MultipleIdentifiers(t *testing.T) {
	input := "cannot convert `foo` (type `int`) to type `string`"
	result := Templatize(input)

	if !result.IsTemplated {
		t.Fatal("期望 IsTemplated=true")
	}
	if len(result.Identifiers) != 3 {
		t.Fatalf("期望 3 个标识符，实际 %d 个", len(result.Identifiers))
	}
	if result.Template != "cannot convert $ID_0$ (type $ID_1$) to type $ID_2$" {
		t.Errorf("模板不匹配: %q", result.Template)
	}
}

func TestTemplatize_NoIdentifiers(t *testing.T) {
	input := "syntax error near unexpected token"
	result := Templatize(input)

	if result.IsTemplated {
		t.Fatal("期望 IsTemplated=false")
	}
	if result.Template != input {
		t.Errorf("无标识符时 Template 应等于原文: %q", result.Template)
	}
}

func TestTemplatize_SameTemplateForDifferentIdentifiers(t *testing.T) {
	// 核心场景：不同标识符的消息应生成相同的模板
	msg1 := "variable `foo` declared but not used"
	msg2 := "variable `bar` declared but not used"

	r1 := Templatize(msg1)
	r2 := Templatize(msg2)

	if r1.Template != r2.Template {
		t.Errorf("模板应相同:\n  msg1 模板: %q\n  msg2 模板: %q", r1.Template, r2.Template)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RestoreIdentifiers 单元测试
// ─────────────────────────────────────────────────────────────────────────────

func TestRestoreIdentifiers_Basic(t *testing.T) {
	translated := "变量 $ID_0$ 已声明但未使用"
	identifiers := []string{"`foo`"}

	result := RestoreIdentifiers(translated, identifiers)
	expected := "变量 `foo` 已声明但未使用"

	if result != expected {
		t.Errorf("还原失败:\n  got:  %q\n  want: %q", result, expected)
	}
}

func TestRestoreIdentifiers_WithSpaces(t *testing.T) {
	// 翻译引擎可能在占位符内添加空格
	translated := "变量 $ ID_0 $ 已声明但未使用"
	identifiers := []string{"`foo`"}

	result := RestoreIdentifiers(translated, identifiers)
	expected := "变量 `foo` 已声明但未使用"

	if result != expected {
		t.Errorf("含空格占位符还原失败:\n  got:  %q\n  want: %q", result, expected)
	}
}

func TestRestoreIdentifiers_MultipleIds(t *testing.T) {
	translated := "无法将 $ID_0$（类型 $ID_1$）转换为类型 $ID_2$"
	identifiers := []string{"`foo`", "`int`", "`string`"}

	result := RestoreIdentifiers(translated, identifiers)
	expected := "无法将 `foo`（类型 `int`）转换为类型 `string`"

	if result != expected {
		t.Errorf("多标识符还原失败:\n  got:  %q\n  want: %q", result, expected)
	}
}

func TestRestoreIdentifiers_Empty(t *testing.T) {
	// 无标识符时应原样返回
	result := RestoreIdentifiers("hello world", nil)
	if result != "hello world" {
		t.Errorf("空标识符列表应原样返回: %q", result)
	}
}

func TestRestoreIdentifiers_OutOfBounds(t *testing.T) {
	// 索引越界时保留占位符
	translated := "变量 $ID_0$ 和 $ID_5$ 错误"
	identifiers := []string{"`foo`"}

	result := RestoreIdentifiers(translated, identifiers)
	expected := "变量 `foo` 和 $ID_5$ 错误"

	if result != expected {
		t.Errorf("越界处理失败:\n  got:  %q\n  want: %q", result, expected)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Templatize + RestoreIdentifiers 端到端测试
// ─────────────────────────────────────────────────────────────────────────────

func TestTemplatize_EndToEnd(t *testing.T) {
	// 模拟完整的模板化翻译流程
	tests := []struct {
		name       string
		original   string
		translated string // 对模板的翻译结果
		want       string // 还原后的期望结果
	}{
		{
			name:       "诊断消息-反引号",
			original:   "variable `myVar` declared but not used",
			translated: "变量 $ID_0$ 已声明但未使用",
			want:       "变量 `myVar` 已声明但未使用",
		},
		{
			name:       "诊断消息-单引号",
			original:   "cannot find name 'MyClass'",
			translated: "找不到名称 $ID_0$",
			want:       "找不到名称 'MyClass'",
		},
		{
			name:       "诊断消息-多标识符",
			original:   "type `string` is not assignable to type `number`",
			translated: "类型 $ID_0$ 不能赋值给类型 $ID_1$",
			want:       "类型 `string` 不能赋值给类型 `number`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := Templatize(tt.original)
			if !tmpl.IsTemplated {
				t.Fatal("期望 IsTemplated=true")
			}
			result := RestoreIdentifiers(tt.translated, tmpl.Identifiers)
			if result != tt.want {
				t.Errorf("端到端结果不匹配:\n  got:  %q\n  want: %q", result, tt.want)
			}
		})
	}
}
