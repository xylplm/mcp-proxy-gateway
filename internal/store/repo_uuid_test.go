package store

import (
	"strings"
	"testing"
	"uuid"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// TestNewUUIDIsTimeOrderedV7 验证主键使用版本 7：既是规范 UUID，又按生成顺序递增。
//
// 时间有序是选用 v7 的目的所在（顺序写入主键索引以减少页分裂），故此处直接对顺序性
// 作断言，避免日后被改回 v4 而无人察觉。
func TestNewUUIDIsTimeOrderedV7(t *testing.T) {
	const n = 64
	ids := make([]string, 0, n)
	for range n {
		id := newUUID()
		if !isUUID(id) {
			t.Fatalf("newUUID 应生成规范 UUID，实际 %q", id)
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			t.Fatalf("newUUID 结果应可解析，实际 %q：%v", id, err)
		}
		// 版本号位于第 3 组的首个十六进制字符。
		if got := strings.Split(parsed.String(), "-")[2][0]; got != '7' {
			t.Fatalf("期望版本 7 UUID，实际 %q（版本字符 %q）", id, string(got))
		}
		ids = append(ids, id)
	}

	// v7 高 48 位为毫秒时间戳，同一毫秒内由随机位递增保序，故整体应单调不降。
	//
	// 断言「不降」而非「严格递增」：标准库对 v7 有序性的保证明确排除系统时钟回拨的情形，
	// 严格递增会让这条用例在极少数时钟回退时误报。
	for i := 1; i < len(ids); i++ {
		if ids[i-1] > ids[i] {
			t.Fatalf("v7 标识不应逆序：第 %d 个 %q 小于前一个 %q", i, ids[i], ids[i-1])
		}
	}
}

// TestIsUUIDAcceptsCanonicalFormOnly 验证只接受规范 36 字符形式。
//
// 标准库 uuid.Parse 额外接受 "{...}" 与 "urn:uuid:..." 写法，它们解析出的标识与裸写法
// 相同。若放行，同一条记录会有多种字符串表示，绕过按串去重与等值判断，故必须拒绝。
func TestIsUUIDAcceptsCanonicalFormOnly(t *testing.T) {
	const canonical = "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"

	accepted := []struct {
		desc  string
		input string
	}{
		{"规范小写形式", canonical},
		{"大写形式（PostgreSQL 比较不区分大小写）", strings.ToUpper(canonical)},
		{"全零 Nil UUID", "00000000-0000-0000-0000-000000000000"},
	}
	for _, tc := range accepted {
		if !isUUID(tc.input) {
			t.Errorf("%s 应被接受：%q", tc.desc, tc.input)
		}
	}

	rejected := []struct {
		desc  string
		input string
	}{
		{"花括号包裹形式", "{" + canonical + "}"},
		{"urn:uuid 前缀形式", "urn:uuid:" + canonical},
		{"缺少分隔符的 32 字符形式", strings.ReplaceAll(canonical, "-", "")},
		{"长度不足", "f81d4fae-7dec-11d0-a765"},
		{"含非十六进制字符", "g81d4fae-7dec-11d0-a765-00a0c91e6bf6"},
		{"前后有空白", " " + canonical + " "},
		{"空串", ""},
	}
	for _, tc := range rejected {
		if isUUID(tc.input) {
			t.Errorf("%s 应被拒绝：%q", tc.desc, tc.input)
		}
	}
}

// TestParseUUIDEmptyAndInvalid 验证 parseUUID 的两个边界：空串视作 SQL NULL 原样返回，
// 非法格式归一化为 VALIDATION 类领域错误而非透传到数据库层。
func TestParseUUIDEmptyAndInvalid(t *testing.T) {
	got, err := parseUUID("")
	if err != nil || got != "" {
		t.Fatalf("空串应原样返回且无错误，实际 got=%q err=%v", got, err)
	}

	const canonical = "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"
	got, err = parseUUID(canonical)
	if err != nil || got != canonical {
		t.Fatalf("合法标识应原样返回，实际 got=%q err=%v", got, err)
	}

	for _, bad := range []string{"{" + canonical + "}", "urn:uuid:" + canonical, "not-a-uuid"} {
		if _, err := parseUUID(bad); err == nil {
			t.Errorf("非法标识应返回错误：%q", bad)
		} else if code := asAPIError(t, err).Code; code != domain.CodeValidation {
			t.Errorf("非法标识 %q 期望错误码 %s，实际 %s", bad, domain.CodeValidation, code)
		}
	}
}

// TestNullableUUIDMapsEmptyToNil 验证可空外键列的映射：空串转 SQL NULL，非空透传。
func TestNullableUUIDMapsEmptyToNil(t *testing.T) {
	ptr, err := nullableUUID("")
	if err != nil {
		t.Fatalf("空串不应报错：%v", err)
	}
	if ptr != nil {
		t.Fatalf("空串应映射为 nil（SQL NULL），实际 %q", *ptr)
	}

	const canonical = "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"
	ptr, err = nullableUUID(canonical)
	if err != nil {
		t.Fatalf("合法标识不应报错：%v", err)
	}
	if ptr == nil || *ptr != canonical {
		t.Fatalf("合法标识应透传，实际 %v", ptr)
	}
}
