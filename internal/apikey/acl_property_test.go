package apikey

import (
	"net/netip"
	"testing"

	"pgregory.net/rapid"
)

// Feature: mcp-proxy-gateway, Property 21: 来源白名单匹配
//
// Validates: Requirements 13.9, 13.10
//
// 对任意请求来源地址与 IP/CIDR 白名单，验证纯函数 MatchCIDR 的三条不变量：
//   - 白名单为空（nil 或空切片，未配置 ACL）时放行任意来源（Req 13.9 的反面：无限制即放行）；
//   - 白名单非空时，当且仅当来源 IP 落在任一条目所表示的网段内才放行——命中放行、未命中拒绝
//     （Req 13.9、13.10）；
//   - 以独立参照判定（netip.Prefix.Contains）对照 MatchCIDR 的结果，二者必须一致。
//
// 生成器同时确定性构造两类场景：
//   - 「IP 必然在某 CIDR 内」：将来源地址按随机前缀长度掩码得到包含它的网段，加入白名单；
//   - 「IP 必然在所有 CIDR 外」：剔除会命中来源的随机网段，并追加一个翻转比特得到的单主机
//     网段（与来源必不相同），保证白名单非空且全部未命中。
func TestProperty21SourceWhitelistMatch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// 生成已归一（Unmap）的来源地址，IPv4/IPv6 均覆盖。
		addr := p21DrawAddr(t, "remote")
		addrStr := addr.String()

		// 不变量一：白名单为空时放行任意来源（nil 与空切片两种形态）。
		for _, empty := range [][]string{nil, {}} {
			ok, err := MatchCIDR(addrStr, empty)
			if err != nil {
				t.Fatalf("空白名单不应返回错误：addr=%s err=%v", addrStr, err)
			}
			if !ok {
				t.Fatalf("未配置白名单时应放行任意来源：addr=%s", addrStr)
			}
		}

		// 生成若干随机「干扰」网段，可能命中也可能不命中来源地址。
		decoyN := rapid.IntRange(0, 4).Draw(t, "decoyN")
		decoys := make([]netip.Prefix, 0, decoyN)
		for range decoyN {
			decoys = append(decoys, p21DrawPrefix(t, "decoy"))
		}

		// 场景一（命中）：构造必然包含来源地址的网段，与干扰项混合后必须放行。
		containing := p21ContainingPrefix(t, addr)
		hitList := append(append([]netip.Prefix{}, decoys...), containing)
		if !p21AnyContains(hitList, addr) {
			// 防御性自检：构造的 containing 必然包含来源，否则生成器有误。
			t.Fatalf("构造的网段应包含来源：addr=%s containing=%s", addrStr, containing)
		}
		ok, err := MatchCIDR(addrStr, p21Strings(hitList))
		if err != nil {
			t.Fatalf("合法白名单不应返回错误：addr=%s list=%v err=%v", addrStr, p21Strings(hitList), err)
		}
		if !ok {
			t.Fatalf("来源落在网段内应放行：addr=%s list=%v", addrStr, p21Strings(hitList))
		}

		// 场景二（未命中）：仅保留不包含来源的干扰项，并追加一个必不命中的单主机网段。
		missList := make([]netip.Prefix, 0, len(decoys)+1)
		for _, d := range decoys {
			if !d.Contains(addr) {
				missList = append(missList, d)
			}
		}
		missList = append(missList, p21DisjointHostPrefix(addr))
		if p21AnyContains(missList, addr) {
			t.Fatalf("未命中场景的白名单不应包含来源：addr=%s list=%v", addrStr, p21Strings(missList))
		}
		ok, err = MatchCIDR(addrStr, p21Strings(missList))
		if err != nil {
			t.Fatalf("合法白名单不应返回错误：addr=%s list=%v err=%v", addrStr, p21Strings(missList), err)
		}
		if ok {
			t.Fatalf("来源不在任何网段内应拒绝：addr=%s list=%v", addrStr, p21Strings(missList))
		}

		// 不变量二：任意非空白名单下，MatchCIDR 结果必须与独立参照判定一致。
		mixed := append([]netip.Prefix{}, decoys...)
		if rapid.Bool().Draw(t, "includeContaining") {
			mixed = append(mixed, containing)
		}
		if len(mixed) > 0 {
			got, gerr := MatchCIDR(addrStr, p21Strings(mixed))
			if gerr != nil {
				t.Fatalf("合法白名单不应返回错误：addr=%s list=%v err=%v", addrStr, p21Strings(mixed), gerr)
			}
			want := p21AnyContains(mixed, addr)
			if got != want {
				t.Fatalf("与参照判定不一致：addr=%s list=%v got=%v want=%v", addrStr, p21Strings(mixed), got, want)
			}
		}
	})
}

// p21DrawAddr 生成一个已归一（Unmap）的随机地址，IPv4 与 IPv6 各占一半概率。
// 归一可消除 IPv4-mapped IPv6 形态，使参照判定与 MatchCIDR 的 Unmap 行为保持一致。
func p21DrawAddr(t *rapid.T, label string) netip.Addr {
	if rapid.Bool().Draw(t, label+"IsIPv4") {
		var b [4]byte
		copy(b[:], rapid.SliceOfN(rapid.Byte(), 4, 4).Draw(t, label+"V4"))
		return netip.AddrFrom4(b)
	}
	var b [16]byte
	copy(b[:], rapid.SliceOfN(rapid.Byte(), 16, 16).Draw(t, label+"V6"))
	return netip.AddrFrom16(b).Unmap()
}

// p21DrawPrefix 生成一个随机网段：取随机地址与随机前缀长度，对齐到网络边界。
func p21DrawPrefix(t *rapid.T, label string) netip.Prefix {
	a := p21DrawAddr(t, label)
	plen := rapid.IntRange(0, a.BitLen()).Draw(t, label+"Plen")
	return netip.PrefixFrom(a, plen).Masked()
}

// p21ContainingPrefix 构造一个必然包含 addr 的网段：以 addr 为基址按随机前缀长度掩码。
// PrefixFrom(addr, plen).Masked() 得到 addr 所属的网络前缀，故其必然包含 addr。
func p21ContainingPrefix(t *rapid.T, addr netip.Addr) netip.Prefix {
	plen := rapid.IntRange(0, addr.BitLen()).Draw(t, "containPlen")
	return netip.PrefixFrom(addr, plen).Masked()
}

// p21DisjointHostPrefix 返回一个必然不包含 addr 的单主机网段。
// 翻转最高比特得到与 addr 必不相同的主机地址，再以满位宽（/32 或 /128）构造主机网段，
// 单主机网段仅包含其自身，故不会命中 addr。
func p21DisjointHostPrefix(addr netip.Addr) netip.Prefix {
	b := addr.AsSlice()
	b[0] ^= 0x80
	flipped, _ := netip.AddrFromSlice(b)
	flipped = flipped.Unmap()
	return netip.PrefixFrom(flipped, flipped.BitLen())
}

// p21AnyContains 为独立参照判定：白名单中是否存在任一网段包含 addr。
func p21AnyContains(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// p21Strings 将网段切片转换为 MatchCIDR 接受的 CIDR 文本列表。
func p21Strings(prefixes []netip.Prefix) []string {
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = p.String()
	}
	return out
}
