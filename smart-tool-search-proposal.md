# Smart 模式工具搜索与召回增强方案

> 对应 issue：[#3 Smart 模式工具搜索与召回能力增强](https://github.com/xylplm/mcp-proxy-gateway/issues/3)
>
> 本文是对原 RFC 的评审与替代方案。原 RFC 的问题诊断准确，但技术选型偏重，且有两条判断与本仓库实际实现不符。下文先给结论，再给核查依据与可落地设计。

## 1. 结论摘要

原 RFC 的四个核心痛点（分词割裂、长查询过严、缩写失配、零结果无引导）确实存在，值得修复。但建议调整方案的重心：

| 原 RFC 主张 | 处置 | 一句话理由 |
|---|---|---|
| ① 查询归一化与分词 | **采纳** | 这是零召回的主因，改动小、收益最大 |
| ② 多词 OR + BM25 评分 | **采纳 OR，替换 BM25** | BM25 的 IDF/长度归一化在"工具名 + 一句话描述"这种超短文档上收益近零，却引入全局统计量的维护与失效成本 |
| ③ Embedding / 向量 / 混合检索 | **不做** | 与本 RFC 自己的目标②"确定性低延迟、不依赖远程模型"矛盾，且破坏自部署零外部依赖的定位 |
| ③ 零结果时 LLM 查询改写 | **不做** | 同上；确定性兜底已能覆盖 |
| ④ 权限闭环 | **已满足，只补回归测试** | 四个网关工具已共用同一条鉴权链路，见 §2.3 |
| ⑤ 兜底建议与分页 | **采纳** | 低成本高收益 |
| 多上游同名工具按标签加权区分 | **前提不成立，需重新定义** | 本仓库在管线阶段 6 已把同名工具归并为单条对外工具，见 §2.4 |

同时补充五项原 RFC 未覆盖、但对 Smart 模式实际体验影响更大的改进：

1. **`list_tools` 在大工具集下不可用**。280 个工具按默认 50 条分页要翻 6 页、消耗 20k-30k tokens，Smart 模式"省上下文"的意义被抵消。应提供按上游分组的概览与过滤（§4.7）。
2. **搜索结果缺少消歧信息**。只回 `name + description`，LLM 无法判断 `list_vms` 来自 PVE 还是别处，也不知道某个工具背后有多个来源、参数结构是否冲突（§4.7）。
3. **描述未截断**。部分上游的工具描述长达数百字，50 条结果可能直接撑爆上下文（§4.6）。
4. **`get_tool` 不支持批量**。LLM 拿到 3 个候选要发 3 次请求，往返成本直接影响体验（§4.7）。
5. **网关工具的描述本身就是召回率的一部分**。LLM 怎么构造查询完全取决于描述怎么写。当前描述说"按关键词检索"，会引导 LLM 发单个词或整句。改文案是零成本改进，收益可能大于算法调优（§4.7）。

另外一个重要修正：**性能瓶颈不在检索算法，在聚合工具集的重复构建**。原 RFC 提出"确定性词法索引"来保障毫秒级响应，但按本仓库规模（数百到数千工具）估算，线性打分本身在微秒级；真正的耗时是每次 `search_tools` 都要重跑一遍 `BuildToolSet`，即 N 个上游的缓存读取加 2N 次规则查询。这个判断由 §8.3 的基准测试验证，不作为既成事实。因此"< 20ms"这条基线要靠**聚合结果短缓存**达成，而不是靠索引结构。详见 §4.8。

## 2. 现状核查

### 2.1 当前检索实现

`internal/mcpapi/smartmode.go` 的 `SearchTools`：

```go
kw := strings.ToLower(strings.TrimSpace(query))
for _, t := range tools {
    if len(out) >= effLimit { break }
    if strings.Contains(strings.ToLower(t.Name), kw) ||
        strings.Contains(strings.ToLower(t.Description), kw) {
        out = append(out, ToolSummary{Name: t.Name, Description: t.Description})
    }
}
```

事实确认：

- 整串子串匹配，**无分词**。`github create pr` 作为一个整体串去匹配，必然零结果。
- **无评分、无排序**。命中顺序等于聚合管线的输出顺序（按上游 `sort_order`），与相关度无关。截断到 limit 时可能把最相关的丢掉。
- **无分页**。`search_tools` 只有 `limit` 截断，没有 cursor（`list_tools` 有基于 offset 的游标）。
- **零结果即空数组**，无任何引导。
- 检索字段只有 `Name` 与 `Description`。`ToolDef.OriginalName` 已在结构体中但未参与检索，意味着别名重写后用户/LLM 用原始名搜不到。

原 RFC 说的"若后端采用严格 AND 匹配"这点需要更正：当前实现不是 AND，是**整串子串**，比 AND 更严格。

### 2.2 数据模型可用字段

`internal/domain/types.go`：

```go
type ToolDef struct {
    OriginalName   string          // 上游原始名，路由依据
    Name           string          // 对外名，可被别名规则改写
    Description    string
    InputSchema    json.RawMessage
    UpstreamID     string
    Order          int
    SourceCount    int             // 该对外工具背后的来源数
    SchemaConflict bool
}
```

- **没有** tags / alias 字段。原 RFC 提到的"工具别名/Tags 权重次之"，在当前模型里无对应数据。
- 上游侧有 `UpstreamConfig.Name` 与 `UpstreamConfig.Tags []string`，但**未下传到 `ToolDef`**。
- `ReverseEntry.Candidates[].UpstreamName` 持有上游名快照（`internal/aggregation/pipeline.go`），`ToolSourceView` 也有 `UpstreamName`，但都不在 `domain.Aggregation_Service` 接口上。

`domain.Aggregation_Service` 只有两个方法：

```go
type Aggregation_Service interface {
    BuildToolSet(ctx, apiKeyID) ([]ToolDef, error)
    InvokeTool(ctx, apiKeyID, exposedName, args) (ToolResult, error)
}
```

`SmartModeHandler` 只依赖这个接口。要让检索看到上游名与标签，需要引入可选窄接口（capability detection），这是本仓库已在 `PreDispatchInvoker`、`UpstreamAvailability`、`RecoveryAwareInvoker`、`ToolResultCacheService` 上反复使用的模式，不算新增架构概念。

### 2.3 权限闭环已经成立

原 RFC 的 ④ 无需新工作。核查结论：

- `ListTools` / `SearchTools` / `GetTool` 三个方法**全部**以 `h.agg.BuildToolSet(ctx, apiKeyID)` 为唯一数据来源；
- `CallTool` 走 `h.agg.InvokeTool`，其内部第一步就是 `buildToolSetWithReverseMap(ctx, apiKeyID)`，不在可见集合内直接返回 `TOOL_NOT_FOUND` 且不向任何上游转发；
- `buildToolSetWithReverseMap`（`internal/aggregation/aggregation.go`）依次执行：上游权限过滤（`upstreamAccessAuthorizer.FilterUpstreams`）、风险档案过滤（`riskAuthorizer.FilterSources`）、MCP 级屏蔽、别名重写、API Key 级屏蔽、同名归并。检索只能在这条管线的**输出**上工作，物理上拿不到被过滤掉的工具。

也就是说"候选集生成后统一过滤"这个要求，本仓库是更强的"过滤后才生成候选集"。**本方案必须守住这条不变量**：所有检索增强只允许作用于 `BuildToolSet` 的返回值，不得为了性能绕过管线直接读缓存。§8.2 有对应回归测试。

### 2.4 "多上游同名"的前提需要修正

`internal/aggregation/pipeline.go` 的 `groupToolsByName` 按最终对外 `Name` 分组，对外**只保留一条**工具定义，多个来源收进 `ReverseEntry.Candidates`，调用时再按路由策略选一个上游。

所以不存在"多个同名工具同时出现在搜索结果里需要按标签区分"的场景。真实的消歧需求是两个：

1. **相似不同名**：`vm_list`（PVE）与 `container_list`（NAS）语义相近，LLM 需要知道各自属于哪个服务才能选对。
2. **单名多来源**：一个对外工具背后有 N 个上游（`SourceCount > 1`），LLM 应当知晓，尤其在 `SchemaConflict` 为真时。

对应做法是在搜索结果里补上游归属与来源数，而不是"按标签加权拆分同名工具"。

### 2.5 契约与 spec 约束

`search_tools` 的语义写进了需求与设计文档，改召回语义必须同步：

- `.kiro/specs/mcp-proxy-gateway/requirements.md` Req 11.4：「SHALL 返回名称或描述中包含查询关键字的聚合工具列表」
- `.kiro/specs/mcp-proxy-gateway/design.md` Property 11：「search_tools 返回的每个工具其名称或描述都包含该关键字」
- `internal/mcpapi/smartmode_property_test.go` 的 `TestProperty11SmartDiscoveryAndGet` 按上述属性断言

分词与同义词展开后，"每个结果都包含原始查询串"不再成立，这三处需要一起改。具体建议见 §6。

## 3. 设计原则

在展开方案前先约定取舍准则，后续每条设计都可回溯到这里：

1. **确定性优先**。相同输入必须得到相同的结果与相同的顺序。这是可测试、可排障、可复现的前提，也是能写属性测试的前提。任何引入非确定性（远程模型、随机采样、时间相关排序）的方案一律排除。
2. **旧行为是新行为的子集**。当前的整串子串匹配作为兜底保留，保证不出现"升级后原来能搜到的搜不到"的回退。
3. **不新增用户可配置项**。评分权重、同义词表、缓存 TTL 全部内置为常量。用户扩展语义的正确入口是**已有的别名规则**（改名 + 改描述），不需要再造一套词典管理界面。
4. **零外部依赖**。不引入检索引擎、不引入分词库、不引入 embedding 模型。检索内核是一个纯函数包，只用标准库。
5. **不绕过聚合管线**。检索只消费 `BuildToolSet` 的输出，权限不变量由管线保证（§2.3）。

## 4. 推荐方案

按投入产出分两个阶段。P0 不触碰 `domain` 接口与 `aggregation` 包，只新增一个纯函数包并改 `mcpapi`，可以独立发布；P1 处理消歧与性能，需要可选接口与缓存。

### 4.1 新增检索内核包 `internal/toolsearch`

纯函数、零依赖（仅标准库）、可独立单测与属性测试。完整对外契约：

```go
package toolsearch

// Doc 是单个工具的检索文档。
//
// 调用方必须保证传入 Build 的 docs 切片与自身的工具切片严格同序且长度相等，
// 因为 Hit.DocIndex 就是 docs 的下标，调用方据此回查原工具定义。
type Doc struct {
    // Name 为对外名（domain.ToolDef.Name）。
    Name string
    // OriginalName 为上游原始名；与 Name 相同时内部自动跳过，不会重复计分。
    OriginalName string
    // Description 为完整描述，内部只取前 descTokenLimit 个词元参与检索。
    Description string
    // UpstreamNames 为该工具全部来源上游的名称（已去重）。P0 阶段传 nil。
    // 注意是复数：同名归并后一个对外工具可能有多个来源上游。
    UpstreamNames []string
    // UpstreamTags 为该工具全部来源上游的标签（已去重）。P0 阶段传 nil。
    UpstreamTags []string
}

// Hit 是一条命中结果及其可解释的评分依据。
type Hit struct {
    // DocIndex 为该命中在 Build 入参 docs 中的下标。
    DocIndex int
    // Score 为加权得分，仅用于排序与排障，不对外暴露给 MCP 客户端。
    Score float64
    // Covered 为命中的「有效查询词元」数量，取值 0..len(有效查询词元)。
    Covered int
    // Matched 为命中的有效查询词元（去重、按查询中首次出现顺序），供日志与测试断言。
    Matched []string
}

// Result 是一次检索的完整结果。
type Result struct {
    // Hits 已按 §4.3 的三级规则排序，并已按 offset/limit 切片。
    Hits []Hit
    // Total 为切片前的命中总数。调用方据此判断是否有下一页：offset+len(Hits) < Total。
    Total int
    // Fallback 为 true 表示本次结果来自整串子串兜底（§4.5 第 2 级）而非词元召回。
    // 供调用方决定提示文案，也便于测试断言走了哪条路径。
    Fallback bool
    // Suggestions 仅在 Total == 0 时非空，为确定性排序的候选关键词（§4.5）。
    Suggestions []string
}

// Index 是预处理好的文档词元集合，可复用于多次查询。并发只读安全。
type Index struct { /* 私有字段 */ }

// Build 预处理文档集合。docs 为 nil 或空时返回非 nil 的空索引，Search 在其上返回零值 Result。
func Build(docs []Doc) *Index

// Search 执行一次检索。
//
//   - query 为原始查询串，内部完成归一化、短语替换、分词、停用词过滤、同义词展开。
//   - limit 必须 > 0，由调用方（SmartModeHandler.resolveLimit）先收敛到 [1,200]。
//     传入 <=0 时内部按 1 处理，不报错。
//   - offset 为切片起点，必须 >= 0。offset >= Total 时返回空 Hits 但 Total 仍为真实总数，
//     与 ListTools「偏移越界收敛到末尾返回空页且不报错」的既有语义保持一致。
//   - 查询归一化后为空（空串、纯空白、纯停用词、纯标点）时返回零值 Result（Total=0、
//     Hits 为空、Suggestions 为空），不报错。注意这与当前实现的行为差异：当前空查询
//     的子串匹配会命中全部工具，见 §4.9。
func (ix *Index) Search(query string, limit, offset int) Result
```

`Build` 与 `Search` 分离，使 P1 的缓存可以只缓存 `*Index`，避免每次查询重新分词。P0 阶段每次现建索引也可接受。

内部常量（全部内置，不可配置）：

```go
const (
    descTokenLimit   = 200  // 描述参与检索的最大词元数
    maxDescriptionRunes = 4096 // 上游描述分词前的安全上限
    maxIndexedFieldRunes = 512 // 工具名、原始名、上游名/标签的索引安全上限
    maxQueryRunes     = 512  // 单次 search_tools 查询的最大字符数
    minPrefixLen     = 3    // 参与前缀匹配的 ASCII 词元最小长度
    synonymDiscount  = 0.6  // 同义词展开命中的折扣
    reversePrefixDiscount = 0.7 // 反向前缀（词形差异）命中的折扣
    maxSuggestions   = 3    // 零结果时返回的候选词数量
)
```

### 4.2 归一化与分词

`Tokenize(s string) []string`，单遍扫描 rune，在以下位置切分：

| 切分规则 | 例子 |
|---|---|
| 非字母数字非 CJK 字符（`_ - . / : 空格` 及标点） | `reach_twitter_user_timeline` → `reach twitter user timeline` |
| 小写到大写边界 | `userTimeline` → `user Timeline` |
| 连续大写后接小写 | `HTTPServer` → `HTTP Server` |
| 字母与数字边界 | `vm100` → `vm 100` |
| CJK 连续段 | 单字成词元 + 相邻二字 bigram |

全部结果小写化，丢弃空词元。CJK 段的处理规则：对连续 CJK 段 `s`，产出每个单字，以及每个相邻二字 bigram。例如"虚拟机"产出 `虚 拟 机 虚拟 拟机`。用 bigram 而非引入分词库，是因为中文查询在工具检索里主要是二字术语（快照、仓库、节点），bigram 足够覆盖且零依赖。三字及以上术语靠 §4.4 的短语替换处理，不依赖分词。

**文档侧词元集**（`Build` 时一次性算好，每个字段一个 `map[string]struct{}`）：

| 字段 | 来源 | 说明 |
|---|---|---|
| `nameTokens` | `Tokenize(Name)` | |
| `origTokens` | `Tokenize(OriginalName)` | 仅当 `OriginalName != Name` 时构建，否则为 nil |
| `descTokens` | `Tokenize(Description)` | 截断到前 `descTokenLimit` 个词元 |
| `upTokens` | `Tokenize(每个 UpstreamNames 与 UpstreamTags 元素)` 的并集 | P0 为 nil |
| `nameRaw` | `lower(strings.TrimSpace(Name))` | 整串，用于精确、前缀、子串判定 |

除 map 外，`Index` 还需维护一张全局词元表用于生成建议：`allTokens map[string]int`，键为所有文档 `nameTokens ∪ origTokens ∪ upTokens` 的词元（**不含 descTokens**，避免建议词被描述里的常用词淹没），值为出现该词元的文档数。

**查询侧停用词过滤**。自然语言长查询会带入大量无信息词，若不过滤，它们会命中大量工具的描述，把 `Covered` 分层彻底污染。规则：

- 只过滤**查询侧**，绝不过滤文档侧（否则名为 `list` 的工具会搜不到）。
- 表内置固定，规模 40 条以内：
  - 英文：`a an the of to in on at for with and or is are be i me my please`
  - 中文单字与 bigram：`的 了 我 你 帮 请 一 个 把 给 帮我 一个 请问 怎么 如何 可以`
- **准入规则：任何可能成为工具名或工具名组成部分的词一律不得入表。** 这条比表的具体内容更重要。`get`、`list`、`help`、`do`、`set`、`run`、`show`、`add` 都是工具名高频词，绝不能进表，否则 `get help` 这类查询会被过滤成空。只有纯语法功能词才入表。实施时建议拿真实工具集扫一遍：若停用词表与任一工具名的词元集有交集，说明表有问题。
- 若过滤后有效词元为空，则**回退使用过滤前的词元**，避免"查询全是停用词"时直接零结果。

过滤后的去重词元序列即「有效查询词元」，`Covered` 的分母就是它的长度。

### 4.3 召回与打分

**召回语义：OR，不做 AND 过滤。** 只要命中任一有效查询词元即进入候选。长查询不会因为某个词没命中而整体归零。

排序靠三级严格确定性比较：

1. **`Covered` 降序**：命中的有效查询词元数量。这是解决长查询的关键，把"过滤"换成"排序"，全覆盖的自然排前，部分覆盖的仍可见。
2. **`Score` 降序**：字段加权得分。
3. **`len(Name)` 升序，再 `Name` 字典序升序**：保证稳定分页与可复现断言。第三级必须存在，否则 `sort.Slice` 对同分项的顺序不确定，分页会漏项。实现用 `sort.SliceStable` 并把三级条件写全。

#### 词元匹配的三种方式与方向

这里的方向必须写死，写反会导致大面积召回失败：

| 方式 | 判定 | 例子 |
|---|---|---|
| 相等 | `qt == dt` | 查 `timeline` 命中 `timeline` |
| 正向前缀 | `strings.HasPrefix(dt, qt)`，即**查询词元是文档词元的前缀** | 查 `time` 命中 `timeline` |
| 反向前缀 | `strings.HasPrefix(qt, dt)`，即**文档词元是查询词元的前缀** | 查 `vms` 命中 `vm` |

**反向前缀是必需的**，它以零语言学假设的方式解决了复数与词形问题。LLM 很可能发 `list vms`、`get containers`、`repositories`，而工具名是 `vm_list`、`container_ls`、`repo_get`。若只做正向前缀，这些查询全部落空。反向前缀命中乘 `reversePrefixDiscount`（0.7），保证精确与正向前缀优先。

两个前缀方式都要求参与匹配的两个词元长度均 `>= minPrefixLen`（3）。这排除了 `a`、`is`、`vm` 这类短词元的噪声前缀匹配（`vm` 会前缀命中 `vmware`、`vmid`、`vmotion`，虽不算错但会稀释排序）。CJK 词元不受此限制，只做相等匹配，不做前缀匹配（中文前缀在语义上无意义）。

#### 字段权重

单个查询词元对某文档的贡献 = 各字段各方式的**最高分**（不累加，避免长描述里重复出现同一词就刷分）：

| 字段 | 相等 | 正向前缀 | 反向前缀（× 0.7） |
|---|---|---|---|
| `nameTokens` | 3.0 | 2.0 | 2.1 |
| `origTokens` | 2.5 | 1.6 | 1.75 |
| `upTokens`（P1） | 2.0 | 1.2 | 1.4 |
| `descTokens` | 1.0 | 0.5 | 0.7 |

反向前缀列的值 = 相等分 × 0.7，实现上不必硬编码这一列，直接乘折扣即可。

#### 整串加成

保证"用户直接给出工具名"必然排第一。用**归一化后的完整查询串** `qraw`（小写、去首尾空白、内部连续空白折叠为单空格）与 `nameRaw` 比较：

| 条件 | 加成 |
|---|---|
| `nameRaw == qraw` | +12.0 |
| `strings.HasPrefix(nameRaw, qraw)` | +6.0 |
| `strings.Contains(nameRaw, qraw)` | +3.0 |

三者互斥，取第一个成立的。加成只影响 `Score`，不影响 `Covered`。

#### 完整打分伪代码

```
Search(query, limit, offset):
    qraw = normalize(query)                       // 小写、trim、折叠空白
    qraw = applyPhraseSynonyms(qraw)              // §4.4 短语替换，在分词前
    rawTokens = dedupKeepOrder(Tokenize(qraw))
    terms = removeStopwords(rawTokens)
    if len(terms) == 0: terms = rawTokens         // 全是停用词则回退
    if len(terms) == 0: return Result{}           // 空查询

    // 为每个有效查询词元预展开同义词，权重 1.0 为原词，synonymDiscount 为展开词
    variants[i] = [{text: terms[i], w: 1.0}] + [{text: syn, w: 0.6} for syn in tokenSynonyms(terms[i])]

    hits = []
    for docIdx, doc in docs:
        covered = 0; score = 0.0; matched = []
        for i, term in terms:
            best = 0.0
            for v in variants[i]:
                best = max(best, v.w * fieldBest(doc, v.text))
            if best > 0:
                covered += 1                      // 靠同义词命中也计入 covered
                score += best
                matched = append(matched, term)   // 记原词，不记展开词
        if covered == 0: continue
        score += wholeStringBonus(doc.nameRaw, qraw)
        hits = append(hits, Hit{docIdx, score, covered, matched})

    if len(hits) == 0:
        // §4.5 第 2 级。内部同样要算出完整命中集再切片，
        // 保证 Result.Total 是切片前的真实总数，否则 nextCursor 会算错。
        return substringFallback(qraw, limit, offset)

    sortHits(hits)                                // 三级规则
    total = len(hits)
    return Result{Hits: slice(hits, offset, limit), Total: total}

fieldBest(doc, term):
    return max over 各字段:
        matchScore(doc.nameTokens, term, 3.0)
        matchScore(doc.origTokens, term, 2.5)
        matchScore(doc.upTokens,   term, 2.0)
        matchScore(doc.descTokens, term, 1.0)

matchScore(tokens, term, base):
    if term in tokens: return base
    if isCJK(term): return 0                      // CJK 不做前缀
    if len(term) < minPrefixLen: return 0
    for dt in tokens:
        if len(dt) < minPrefixLen: continue
        if hasPrefix(dt, term): return base * (2.0/3.0)      // 正向前缀
        if hasPrefix(term, dt): return base * 0.7            // 反向前缀
    return 0
```

`matchScore` 里正向前缀写成 `base * (2.0/3.0)` 是为了让 name 字段得到 2.0、orig 得到约 1.67、desc 得到约 0.67，与上表基本一致。若希望完全按上表取值，改为查表即可，两种都可接受，但**必须在实现里固定一种**，否则测试期望值无法确定。建议直接用系数，代码更短。

**关键点**：靠同义词展开命中的词元也要 `covered += 1`。否则 `pr` 查询命中 `create_pull_request` 时 `Covered` 为 0，会被第一级排序压到最后，同义词表就白做了。这是最容易写错的一处。

`matched` 记录的是原始查询词元而非展开词，这样测试可以稳定断言，日志也更容易读。

#### 为什么不用 BM25

BM25 的两项核心贡献是 IDF（罕见词更重要）与文档长度归一化。工具检索的语料是几百到几千条、每条十几到几十个词元的超短文档，IDF 的判别力很弱（几乎每个词的 df 都是个位数），长度归一化的差异也被字段权重覆盖了。代价却是要维护全局 df 与 avgdl 两个统计量，且工具集每次同步都会让它们失效。字段加权加覆盖率分层在这个规模下效果相当，实现量少一个数量级，并且每一条命中都能解释清楚（`Hit.Matched` 直接给出命中词元）。

### 4.4 同义词与缩写

只展开查询侧，**绝不展开文档侧**（避免索引膨胀与噪声召回）。展开词贡献乘 `synonymDiscount`（0.6），避免同义词劫持排序。

关键实现细节：**这是两张独立的表，作用在流水线的不同阶段，不能合并成一张。** 混为一谈是本方案最容易踩的坑，会导致所有多词与中文术语永远匹配不到。

#### 表一：短语表 `phraseSynonyms`，作用在分词之前

`map[string]string`，键是多词短语或多字术语，值是替换后的等价词串。在归一化后的完整查询串上做**子串替换**（不是词边界替换，因为中文没有空格），按键长度降序依次尝试，保证长键优先。

**替换时值的两侧必须各补一个空格。** 这是必须写死的实现细节：中文查询"配置文件"会先把"配置"替换为 `config`，若不补空格就得到 `config文件`，再把"文件"替换为 `file` 后变成 `configfile`，产出一个不存在的词元，两个词都匹配不到。正确做法是替换为 `" config "`，最终得到 `config file`，分词后是两个正确词元。实现上统一在替换函数里包空格，再由归一化折叠连续空白，不要在表的每个值里手写空格。

```go
var phraseSynonyms = map[string]string{
    "pull request":  "pr pull request",
    "merge request": "mr merge request",
    "check run":     "ci check run",
    "virtual machine": "vm qemu virtual machine",
    "虚拟机": "vm qemu",
    "拉取请求": "pull request pr",
    "合并请求": "merge request mr",
    "容器":   "container lxc docker",
    "仓库":   "repo repository",
    "快照":   "snapshot",
    "备份":   "backup",
    "节点":   "node",
    "存储":   "storage disk",
    "磁盘":   "disk",
    "网络":   "network",
    "权限":   "permission acl",
    "目录":   "directory folder dir",
    "文件":   "file",
    "任务":   "task job",
    "状态":   "status state",
    "配置":   "config",
    "日志":   "log",
    "用户":   "user",
    "媒体":   "media video movie",
    "监控":   "monitor metrics",
    // 动作类
    "列出": "list", "列表": "list", "查看": "get list",
    "查询": "query search", "搜索": "search query", "获取": "get",
    "创建": "create", "新建": "create", "删除": "delete remove",
    "更新": "update", "修改": "update",
    "启动": "start", "停止": "stop", "重启": "restart",
}
```

替换保留原短语（如 `pull request` → `pr pull request`），这样原词与缩写都能命中。中文键的值不保留原中文，因为中文词元本身不会出现在英文工具名里，保留只会浪费一个 `Covered` 名额。

**为什么中文必须走这张表**：中文分词产出的是单字与 bigram。"虚拟机"分词后是 `虚 拟 机 虚拟 拟机`，没有"虚拟机"这个三字词元，所以词元级同义词表永远匹配不到它。只有在分词前做子串替换才有效。同理"列出虚拟机的快照"这种连写串，也只有子串替换能处理。

#### 表二：词元表 `tokenSynonyms`，作用在分词之后

`map[string][]string`，键与值都是单个词元。

```go
var tokenSynonyms = map[string][]string{
    "pr":   {"pull", "request"},
    "mr":   {"merge", "request"},
    "repo": {"repository"},
    "repository": {"repo"},
    "vm":   {"qemu", "virtual", "machine"},
    "qemu": {"vm"},
    "ct":   {"container", "lxc"},
    "lxc":  {"container", "ct"},
    "k8s":  {"kubernetes"},
    "kubernetes": {"k8s"},
    "db":   {"database"},
    "database": {"db"},
    "env":  {"environment"},
    "cfg":  {"config", "configuration"},
    "conf": {"config", "configuration"},
    "config": {"cfg", "configuration"},
    "auth": {"authentication", "authorization"},
    "snap": {"snapshot"},
    "ns":   {"namespace"},
    "svc":  {"service"},
    "dir":  {"directory", "folder"},
    "folder": {"dir", "directory"},
    "rm":   {"delete", "remove"},
    "del":  {"delete", "remove"},
    "ls":   {"list"},
    "img":  {"image"},
    "vol":  {"volume"},
    "msg":  {"message"},
    "desc": {"description"},
}
```

注意 `pr → {pull, request}` 展开成两个词元，实现上 `variants` 里要为每个展开词元各建一项，`fieldBest` 取最高值。`create pr` 查 `create_pull_request` 的过程：`create` 相等命中 name（3.0），`pr` 经展开后 `pull` 与 `request` 都相等命中 name，取 `0.6 × 3.0 = 1.8`，`Covered = 2`（满覆盖）。

**表不做双向自动推导**，手写两个方向。自动推导会在 `pr → {pull, request}` 这种一对多上产生错误的反向映射（`pull → pr` 合理，但 `request → pr` 会让所有含 request 的查询都召回 PR 相关工具）。手写虽啰嗦但可控。

两张表合计控制在 60 条以内。

#### 中文支持的边界

工具名与描述绝大多数是英文，纯词法检索无法真正理解中文语义。上表只覆盖高频运维术语，属于**有限支持，不承诺"中文语义搜索"**。这一点要写进文档与工具描述，避免用户预期错位。

需要更好中文体验的用户，正确路径是用**已有的别名规则**把常用工具的描述改成中文，这样中文描述本身就进了 `descTokens`，中文查询直接命中。这也是 §3 原则 3「不新增配置项」的落点：语义扩展的入口已经存在，不需要再造词典管理界面。

### 4.5 零结果兜底

三级降级，前一级有结果就不进入下一级。

**第 1 级：词元 OR 召回**（§4.3）。

**第 2 级：整串子串匹配**。对 `qraw` 做 `strings.Contains(lower(Name), qraw) || strings.Contains(lower(Description), qraw)`，即**与当前实现完全一致的逻辑**。命中后按 `len(Name)` 升序、`Name` 字典序升序排序（当前实现无排序，这里补上以保证分页稳定），置 `Result.Fallback = true`。

保留这一级的目的是让**旧行为成为新行为的子集**（§3 原则 2）。理论上第 1 级召回是第 2 级的超集（子串命中必然有词元命中），但边界情况下不一定，例如查询含标点被分词全部丢弃、或查询是文档词元的中缀（查 `ull` 命中 `pull`，词元匹配不到但子串能到）。留着这一级成本极低，能彻底消除回退风险。

**第 3 级：候选关键词建议**。算法必须确定性，按顺序尝试：

1. 对每个有效查询词元 `t`（长度 >= 2），在 `Index.allTokens` 里找**以 `t` 为前缀**的词元，全部收集。
2. 若第 1 步为空，找与 `t` 的 Levenshtein 距离 <= 2 的词元（仅对 `len(t) >= 4` 的词元做，短词编辑距离噪声太大），全部收集。
3. 对收集到的候选词元按「文档频次降序 → 词元长度升序 → 字典序升序」排序，取前 `maxSuggestions`（3）个。
4. 若仍为空，返回工具数最多的前 3 个上游名（P1，需要 `UpstreamNames`）；P0 阶段返回空数组。

第 3 步的三级排序必须写全，否则 map 遍历顺序会让建议每次不同，属性测试无法断言。Levenshtein 用标准动态规划实现，约 20 行，不引入依赖；`allTokens` 规模在数千量级，全表扫描一次是微秒级。

返回给 LLM 的零结果响应形如：

```json
{
  "tools": [],
  "suggestions": ["timeline", "twitter", "tweet"],
  "hint": "未匹配到工具。可尝试上述关键词，或调用 list_tools 查看有哪些上游服务。"
}
```

`hint` 是**固定文案常量**，不是动态生成的自然语言，保持确定性。`suggestions` 为空时 `hint` 换成另一条固定文案（提示改用 `list_tools` 浏览），两条文案都定义为包级常量。

### 4.6 描述截断

`ToolSummary.Description` 按 **240 个字符**截断（按 rune 计，超出补 `…`）。

理由直接来自 Smart 模式的存在意义：默认 limit 是 50，若上游描述平均 300 字，一次 `search_tools` 就要吐 15k 字符。这与"节省上下文窗口"的目标直接冲突。完整描述由 `get_tool` 提供，职责本来就是分开的。

截断只作用于**返回给客户端的摘要**，不影响检索（检索用描述开头的前 200 个词元）。为防异常上游返回超长描述造成一次查询的大量分配，分词前还要按 rune 截到内置的 4096 字符；工具名、原始名、上游名和标签也只取前 512 rune 建索引。原始元数据不被改写，只有超出资源边界的尾部不能参与检索；这是资源保护，不暴露为配置项。

### 4.7 网关工具契约变更

保持向后兼容，只做加法。现有字段语义与结构不变，已有 e2e 用例（`internal/mcpapi/transports_e2e_test.go`）不受影响。

**`search_tools`**

```jsonc
// 入参（新增 cursor；query 最多 512 个字符）
{ "query": "github create pr", "limit": 50, "cursor": "50" }

// 出参（新增 upstream / sourceCount / nextCursor / suggestions）
{
  "tools": [
    { "name": "create_pull_request", "description": "...", "upstream": "GitHub", "sourceCount": 1 }
  ],
  "nextCursor": "50",
  "suggestions": []
}
```

服务端必须独立校验这些边界，不能只依赖客户端遵守 JSON Schema：`query` 最多 512 rune、`upstream` 最多 256 rune、游标最多 20 字节，非法值在读取聚合集前返回 `VALIDATION`。

字段分期与依据：

| 字段 | 阶段 | 数据来源 |
|---|---|---|
| `nextCursor` | P0 | `Result.Total` 与 offset 计算，见下 |
| `suggestions` / `hint` | P0 | `Result.Suggestions`（§4.5） |
| `sourceCount` | **P0** | `domain.ToolDef.SourceCount`，**字段已存在**，无需可选接口 |
| `schemaConflict` | **P0** | `domain.ToolDef.SchemaConflict`，同上，仅在为 true 时输出 |
| `upstream` | P1 | 需要可选接口拿 `UpstreamName`（§2.2） |

`sourceCount` 与 `schemaConflict` 在 P0 就能提供，这是先前分期判断的修正：`ToolDef` 结构体里这两个字段已经由聚合管线填好了（`groupToolsByName` 里设置），`BuildToolSet` 直接返回。只有上游**名称**需要 P1 的可选接口。

`sourceCount > 1` 告知 LLM 该工具有多个来源上游；配合 `schemaConflict: true` 提示"不同来源的参数结构不一致，调用前务必用 get_tool 确认"。这解决 §2.4 的消歧需求，且不破坏对外工具名唯一的既有不变量。

**`nextCursor` 的计算与游标语义**

与 `list_tools` 完全一致，用十进制偏移量编码，便于复用同一套解析与校验：

```go
offset := 0
if cursor != "" {
    parsed, err := strconv.Atoi(cursor)
    if err != nil || parsed < 0 {
        return domain.NewValidationError("分页游标非法",
            map[string]string{"cursor": "游标必须为非负整数"})
    }
    offset = parsed
}
res := index.Search(query, effLimit, offset)
if offset+len(res.Hits) < res.Total {
    nextCursor = strconv.Itoa(offset + len(res.Hits))
}
```

非法游标返回校验错误（与 `ListTools` 一致），越界游标返回空页不报错。

**分页稳定性的已知限制**：offset 游标在两次请求之间工具集发生变化（同步完成、规则变更）时可能漏项或重项。这是可接受的，理由有三：`list_tools` 已经是同样的语义、检索排序在同一工具集上是严格确定的、LLM 极少翻超过两页。不引入快照式游标，那需要服务端保存会话状态，成本远超收益。

**`list_tools`**

分页语义完全不变，新增两项（均为 P1，都依赖上游归属信息）：

```jsonc
// 入参新增 upstream 过滤
{ "cursor": "", "limit": 50, "upstream": "PVE" }

// cursor 为空（即第一页）时，响应附加上游概览
{
  "tools": [ /* 原有分页结果，语义与顺序完全不变 */ ],
  "nextCursor": "50",
  "upstreams": [
    { "name": "PVE", "toolCount": 283 },
    { "name": "GitHub", "toolCount": 46 }
  ]
}
```

两处语义必须写死，否则实施会走偏：

- **`upstreams` 的返回条件是 `cursor == ""`，不是"首次调用"**。服务端无会话状态，无法判断是否首次。cursor 为空即第一页，是唯一可判定且稳定的条件。翻页时不重复返回概览，避免每页浪费 tokens。
- **`upstreams` 排序**：`toolCount` 降序 → `name` 字典序升序。必须确定，否则每次调用顺序不同。
- **`upstream` 过滤的匹配语义**：对多来源工具（`SourceCount > 1`），只要**任一来源**上游名匹配即保留。匹配方式为「不区分大小写的全等」优先，若无任何工具命中则退化为「不区分大小写的子串包含」，这样 LLM 传 `pve` 也能命中名为 `PVE 生产集群` 的上游。传入的 `upstream` 不匹配任何上游时返回空页 + `upstreams` 概览（帮 LLM 纠正），不报错。

有了 `upstreams` 概览，LLM 的第一步开销从"翻 6 页、20k+ tokens"降到"一次调用、不到 1k tokens"，并且天然获得"按服务定位"的心智模型，顺带满足了原 RFC ⑤ 想要的"推荐相近分类"。

**`get_tool`**

支持批量，`name` 与 `names` 二选一（P1）：

```jsonc
// 入参
{ "names": ["create_pull_request", "create_issue"] }

// 出参（批量形式）
{
  "tools": [ { "name": "...", "description": "...", "inputSchema": { } } ],
  "notFound": ["create_issue"]
}
```

语义约定：

- 只传 `name`（单值）时，返回体与现在完全一致（直接是单个工具定义的 JSON），不包装。这保证现有客户端与 `smartmode_test.go` 的断言不变。
- 传 `names` 时返回上述包装结构。**部分不存在不报错**，可见的放 `tools`，不可见的放 `notFound`。这与单值形式"不可见返回 `TOOL_NOT_FOUND` 错误"不同，是刻意的：批量场景下让 LLM 拿到能拿的、并明确知道哪些拿不到，比整批失败有用。
- `names` 长度必须为 1 至 20；超出直接返回字段级校验错误，不返回部分结果或回显超长输入。这样客户端能立即修正请求，也避免恶意输入把 `notFound` 响应放大。
- `names` 为空数组、同时传 `name` 与 `names`、或两者都为空均返回校验错误；JSON Schema 使用 `oneOf` 与 `minItems` 表达同一契约。
- `notFound` 不泄露任何额外信息，只回显客户端自己传入的名字，因此不违反 §2.3 的可见性约束。

LLM 拿到 3 个候选时省掉 2 次往返。

**工具描述文案**

`GatewayToolSearchToolsDescription` 需要重写，明确告知 LLM 可以用多关键词与自然语言。这是**零成本的召回率提升**，比算法调优更直接：模型的查询构造方式本身就是召回率的一部分。建议表述要点：支持空格分隔的多个关键词、按相关度排序、不要求全部命中、零结果时会给出候选关键词。

同理 `GatewayToolListToolsDescription` 需要说明可按 `upstream` 过滤，引导 LLM 不要盲目翻页。

### 4.8 性能：聚合结果与索引短缓存

这是"< 20ms"基线的真正决定因素。

现状：每次 `search_tools` 都会完整执行 `buildToolSetWithReverseMap`，即

- 1 次 `ListUpstreams`
- 每个启用上游：1 次工具缓存读取（Redis 命中或回源 PG）+ 1 次别名查询 + 1 次屏蔽规则查询
- API Key 非空时再 1 次 API Key 屏蔽规则查询
- 然后跑一遍完整六阶段管线

10 个上游就是 30+ 次 IO。相比之下，§4.3 的打分在 300 工具 × 12 个展开查询词下只有几千次 map 查找，微秒量级。**优化索引结构是错的方向，缓存构建结果才是对的方向。**

建议在 `internal/aggregation` 增加进程内短缓存，与 `docs/optimization-roadmap.md` P2 第 11 条「聚合工具集短缓存」合并落地：

- key：`apiKeyID`（空串代表全局视角，小智接入与管理台都走这个 key）
- value：`{tools []domain.ToolDef, reverse map[string]ReverseEntry, builtAt time.Time}`；首次发现时再惰性补充来源投影，首次搜索时再惰性补充 `*toolsearch.Index`
- TTL：**5 秒**，内置常量，不暴露配置
- 并发：`sync.RWMutex` 保护；允许两个请求同时未命中各建一次，不做 singleflight（构建幂等，重复构建的浪费远小于引入 singleflight 的复杂度）
- 容量：固定最多 32 个 API Key 视角；读取/写入时清理过期项，满时淘汰最早构建项。索引可能较大，宁可少量重建也不能让 API Key churn 无界占用内存。

检索索引（`*toolsearch.Index`）随同一条缓存项缓存，避免每次搜索重新分词；`list_tools` 与 `get_tool` 不创建索引，`get_tool` 直接使用 `BuildToolSet`，不读取工具策略详情。

#### 失效策略：按写入边界立即失效，TTL 仅兜底

可见工具集是权限、别名、上游配置和风险评级的派生结果。正常管理操作不能等待 TTL，否则用户刚保存规则却仍看到旧发现结果，会造成明显的体验与安全困惑。失效接线应按职责边界集中，而不是把缓存细节扩散到业务实现中：

- 工具列表替换（手动刷新与周期同步共用写入路径）统一经过 `domain.Tool_Cache` 装饰器；
- 上游创建、更新、启停、重排、删除集中由 `manager.Manager` 的可选回调失效；
- HTTP 管理层成功写入别名、MCP/API Key 屏蔽规则、API Key 风险档案与上游范围、手工风险覆写、风险目录导入时，通过 `Router.invalidateToolSetCache()` 探测并调用聚合服务的可选能力；
- AI 风险评估成功落库与同步后的风险目录 reconciliation 由风险治理/目录观察器回调失效。

工具策略不改变 discovery 可见集合，不能为此失效工具集缓存；但策略改变了结果是否可复用及其有效期，成功写入时须独立清理工具结果缓存。工具集失效也须同步清理结果缓存，因为同一对外名可能已路由到不同来源。

```go
// 在 internal/aggregation 中新增
//
// NewInvalidatingCache 返回一个装饰后的工具缓存：任何成功的整列表替换或删除都会
// 清空 svc 的聚合工具集短缓存。装配层应把装饰后的实例传给 sync 与 manager，
// 而把原始实例传给 NewService，避免循环依赖。
func NewInvalidatingCache(inner domain.Tool_Cache, svc *Service) domain.Tool_Cache
```

装配顺序（`internal/app/build.go`）：

```go
agg := aggregation.NewService(toolCache, ...)        // 用原始 cache
invalidating := aggregation.NewInvalidatingCache(toolCache, agg)
// 之后把 invalidating 传给 sync.NewPeriodicSyncer / sync.NewRefresher；
// manager 使用原始工具缓存，并以 WithToolSetCacheInvalidator(agg.InvalidateToolSetCache)
// 在上游创建、更新、启停、重排、删除成功后立即失效。
```

5 秒 TTL 仅用于进程外直接写库、异常绕过上述边界或未来未知写入点的最终兜底，不是正常管理操作的生效机制。风险档案与上游授权在真实调用前仍会再次实时授权，缓存绝不能绕过调用权限。

**已知约束（必须在代码注释里写明）**：`buildToolSetWithReverseMap` 有副作用，会重写 `s.upstreamConfigs`（供调用路由读限流配置）。缓存命中时该 map 不刷新，5 秒窗口内可接受。**禁止在未重新分析这条副作用的前提下加长 TTL**，否则会引入限流配置陈旧这类难查的问题。缓存实现处与 `upstreamConfigs` 赋值处都要留注释交叉指向。

不做 Redis 分布式缓存。当前是单进程架构，进程内 map 更简单也更快。

### 4.9 空查询的行为变更

这是一处需要显式决策的行为差异，实施时不要含糊处理。

当前实现：`query` 为空或纯空白时，`kw` 为空串，`strings.Contains(anything, "")` 恒为 true，因此**返回前 limit 个工具**。现有测试 `smartmode_test.go` 中有一条用例（`h.SearchTools(ctx, "", "   ", 0)`）依赖这个行为。

新实现有两个选择：

- **方案 A（推荐）**：空查询返回空列表 + `hint` 引导改用 `list_tools`。理由：`search_tools` 的 `query` 在 JSON Schema 里是 `required`，空查询是客户端用法错误；返回"前 50 个工具"是一种误导性成功，LLM 会以为这就是全部工具。明确的空结果加引导更有帮助。
- **方案 B**：保持现状，空查询等价于 `list_tools` 首页。

**选 A。** 需要同步修改上述测试用例的期望值，并在 §6 的 Req 11.5 附近补一句空查询语义。若实施者选 B，必须在 `Search` 里对空查询走第 2 级兜底路径，不能让它落到"返回零值 Result"，否则两条路径的行为会不一致。

## 5. 明确不做的事项

列出来是为了避免后续被反复提起，也为了让"不过度设计"这条原则有具体落点。

| 不做 | 理由 |
|---|---|
| Embedding / 向量检索 / 混合检索 | 需要模型（本地推理增加镜像体积与内存，远程 API 引入网络延迟、成本与可用性依赖）、需要预计算与失效管理、结果不确定。与原 RFC 目标②"确定性低延迟、不依赖远程改写"直接冲突。数百到数千工具的规模下，词法检索加同义词已能覆盖绝大多数查询 |
| BM25 全局统计打分 | 超短文档语料上 IDF 判别力弱，收益不足以偿付 df/avgdl 的维护与失效成本，见 §4.3 |
| 零结果时调用 LLM 改写查询 | 引入非确定性、外部依赖与不可控延迟。§4.5 的确定性兜底已能给出可操作引导 |
| 引入 bleve / Elasticsearch / PG 全文检索 | 数据量差三到四个数量级，属于用集群方案解个人部署问题。且 PG 全文检索无法表达"必须先过权限管线"这条不变量 |
| 用户可配置的同义词词典与管理界面 | 用户不会去填词典。已有别名规则就是用户可控的语义层，改名与改描述天然进入检索文档，不需要第二套机制 |
| 暴露评分权重、TTL 等调优参数 | 增加理解成本，换来的调参收益微乎其微。内置常量即可 |
| 把同名工具按上游拆分展示 | 与管线阶段 6 的归并设计冲突，会破坏对外工具名唯一这条既有不变量（`TestProperty1AggregatedNamesAreGroupedByName`）。消歧改用 `upstream` / `sourceCount` 字段解决，见 §2.4 |

## 6. spec 文档同步修改

本仓库是 spec 驱动的，改召回语义必须同步三处，否则属性测试与需求会自相矛盾。

**`.kiro/specs/mcp-proxy-gateway/requirements.md` Req 11.4** 建议改为：

> 4. 在智能模式下，WHEN 外部 AI 服务调用工具发现网关工具时，THE MCP API 服务 SHALL 对查询关键字做归一化与分词后，返回名称、上游原始名称或描述命中任一查询词元的聚合工具列表，并按命中词元数量与字段权重降序排序，返回数量默认为 50 条且可在 1 至 200 条范围内配置。

**Req 11.5** 建议补充空查询语义（§4.9）：

> 5. 在智能模式下，IF 外部 AI 服务调用工具发现网关工具且当前可见聚合工具集合中无任何工具命中查询词元，或查询关键字经归一化后为空，THEN THE MCP API 服务 SHALL 返回空列表而非返回错误。

新增一条 Req 11.10：

> 10. 在智能模式下，WHEN 工具发现网关工具的查询无任何命中时，THE MCP API 服务 SHALL 在空列表之外返回候选关键词建议与固定引导文案，且相同输入的建议内容与顺序完全一致。

**`.kiro/specs/mcp-proxy-gateway/design.md` Property 11** 建议改为：

> *For any* 可见工具集合与查询关键字，search_tools 返回的每个工具其名称、上游原始名称或描述中至少命中一个查询词元（或整串子串命中）；结果按「命中词元数降序、加权得分降序、名称长度升序、名称字典序升序」严格排序，同一输入的排序完全确定；返回数量不超过配置上限（默认 50，范围 1-200）；当无任何工具命中时返回空列表而非错误。

**`internal/mcpapi/smartmode_property_test.go`** 的 `TestProperty11SmartDiscoveryAndGet` 按上述新属性重写断言。新属性比旧属性更强：除了包含关系，还断言了排序的确定性，这正好是分词方案最需要锁住的性质。

`design.md` 的「智能模式：网关工具与按需工具发现」小节中的网关工具表格也要同步 §4.7 的入参出参变化。

## 7. 实施计划

### 阶段一（P0）：检索质量

不触碰 `domain` 接口与 `aggregation` 包，无前端改动，可独立发布验证。

**新增文件**

| 文件 | 内容 | 关键导出 |
|---|---|---|
| `internal/toolsearch/doc.go` | 包注释，说明设计取舍与不变量 | |
| `internal/toolsearch/tokenize.go` | 归一化与分词（§4.2） | `Tokenize`（导出以便测试） |
| `internal/toolsearch/stopword.go` | 查询侧停用词表与过滤（§4.2） | 包内 |
| `internal/toolsearch/synonym.go` | 两张同义词表与展开（§4.4） | 包内 |
| `internal/toolsearch/index.go` | `Doc` / `Hit` / `Result` / `Index`、`Build`、`Search`、打分排序（§4.3）、兜底（§4.5） | `Doc` `Hit` `Result` `Index` `Build` `Search` |
| `internal/toolsearch/suggest.go` | 候选词建议与 Levenshtein（§4.5 第 3 级） | 包内 |
| `internal/toolsearch/*_test.go` | 单测 + 属性测试 + 基准（§8） | |

**修改文件**

`internal/mcpapi/smartmode.go`

- `SearchTools` **签名变更**：
  ```go
  // 旧
  func (h *SmartModeHandler) SearchTools(ctx context.Context, apiKeyID, query string, limit int) ([]ToolSummary, error)
  // 新
  func (h *SmartModeHandler) SearchTools(ctx context.Context, apiKeyID, query, cursor string, limit int) (SearchPage, error)
  ```
- 新增 `SearchPage` 类型：
  ```go
  type SearchPage struct {
      Tools       []ToolSummary `json:"tools"`
      NextCursor  string        `json:"nextCursor,omitempty"`
      Suggestions []string      `json:"suggestions,omitempty"`
      Hint        string        `json:"hint,omitempty"`
  }
  ```
- `ToolSummary` 新增 `SourceCount int` 与 `SchemaConflict bool`（均 `omitempty`），取自 `domain.ToolDef` 的同名字段。
- 新增 `truncateDescription(s string, limit int) string`，按 rune 截断到 240 并补 `…`；在 `toToolSummaries` 与搜索结果构建处统一调用（§4.6）。
- 新增 `parseCursor(cursor string) (int, error)`，把 `ListTools` 里现有的游标解析逻辑抽出来复用，两处共用同一份校验与错误文案。
- 重写 `GatewayToolSearchToolsDescription` 与 `GatewayToolListToolsDescription`（§4.7）。
- 更新 `searchToolsInputSchema`，加入 `cursor` 属性。

`internal/mcpapi/service.go`

- `searchToolsArgs` 新增 `Cursor string \`json:"cursor"\``。
- `handleGatewaySearchTools` 改为直接 `jsonResult(page)`，删除现有的 `struct{Tools []ToolSummary}` 匿名包装（`SearchPage` 已含 `tools` 字段，JSON 结构对客户端保持兼容）。

**必须同步修改的现有测试**（不改会编译失败或断言失败）

| 位置 | 数量 | 原因 |
|---|---|---|
| `internal/mcpapi/smartmode_test.go` | 8 处 `h.SearchTools(...)` 调用 | 签名新增 `cursor` 参数、返回类型改为 `SearchPage` |
| `internal/mcpapi/smartmode_test.go` 空查询用例 | 1 处 | 空查询语义变更（§4.9 方案 A） |
| `internal/mcpapi/smartmode_property_test.go` | 1 处调用 + Property 11 断言 | 按 §6 新属性重写 |
| 描述断言相关用例 | 需全文检查 | 描述截断可能影响长描述的相等断言 |

`internal/mcpapi/transports_e2e_test.go` 必须在三种传输下覆盖 `list_tools`、`search_tools`、批量 `get_tool` 与 `call_tool`，不能只验证旧路径。

**spec 同步**：`requirements.md`（Req 11.4 改写 + 新增 11.10）、`design.md`（Property 11 与网关工具表格）、`tasks.md`（新增任务项并标注 Requirements 编号，保持 spec 三件套一致）。

### 阶段二（P1）：消歧与性能

| 落地模块 | 内容 |
|---|---|
| `internal/domain/types.go` | `ToolSourceView` 新增 `UpstreamTags []string \`json:"upstreamTags,omitempty"\``；新增轻量 `ToolDiscovery`（只读投影，不携带 `inputSchema`） |
| `internal/aggregation/aggregation.go` | `BuildToolDetails` 从已缓存的 `upstreamConfigs` 填充 `UpstreamTags`；新增不读策略的 `BuildToolDiscoveries`、惰性索引短缓存与 `InvalidateToolSetCache()`（§4.8） |
| `internal/aggregation/cache.go`（新增） | `NewInvalidatingCache` 缓存装饰器（§4.8） |
| `internal/app/build.go` | 原始 cache 给 `NewService` 与 `manager`；装饰后的给同步服务；manager 注入聚合集失效函数 |
| `internal/mcpapi/smartmode.go` | 定义可选窄接口 `toolDiscoverySource` / `toolSearchSource`；检索文档补 `UpstreamNames` / `UpstreamTags`；`ToolSummary` 补 `Upstream string` |
| `internal/mcpapi/smartmode.go` | `ListTools` 增加 `upstream` 过滤与 `upstreams` 概览；`GetTool` 支持批量 `names`（§4.7） |
| `internal/mcpapi/service.go` | `listToolsArgs` 加 `Upstream`；`nameArg` 加 `Names []string` |
| `web/src/api/tools.ts` | `ToolSource` 接口新增 `upstreamTags?: string[]`，与后端字段对齐 |
| `web/src/views/APIServiceView.vue` | 智能模式说明文案同步新能力 |

可选窄接口按仓库既有模式定义在 `mcpapi` 包内，用类型断言探测：

```go
// toolDiscoverySource 是智能模式发现可选依赖：提供不含策略详情的来源投影。
// 未实现该接口时（如单元测试的 fake），检索退化为只用名称与描述，功能不受影响。
type toolDiscoverySource interface {
    BuildToolDiscoveries(ctx context.Context, apiKeyID string) ([]domain.ToolDiscovery, error)
}
```

`search_tools` 额外探测 `toolSearchSource` 取得缓存索引；`list_tools` 只取来源投影，`get_tool` 直接回落 `BuildToolSet`。`toolDetailSource` 仅保留为旧实现的兼容退化路径，不进入生产发现热路径。

**注意 `BuildToolDetails` 比 `BuildToolSet` 重**（额外读工具策略、算降级状态），因此 Smart 模式不得用它作为生产发现热路径；轻量来源投影既复用同一权限管线，也不引入策略 IO。

管理台的工具目录搜索可以复用 `internal/toolsearch`，但属于 `docs/optimization-roadmap.md` 候选池 C 的范围，不纳入本方案，避免范围膨胀。

小智接入通过 `mcpService.BuildServerWithSource(ctx, "", mode, "xiaozhi")` 复用同一个 `mcpapi.Service`（见 `internal/app/build.go`），两个阶段的改进对 `xiaozhi.mode = smart` 自动生效，无需额外工作。其 `apiKeyID` 为空串，走全局视角缓存。

## 8. 验证与回归

### 8.1 原 RFC 的五个场景

| 场景 | 期望 | 覆盖方式 |
|---|---|---|
| `github create pr` 命中 `create_pull_request` | `pr` 经同义词展开为 `pull request`，`create` 命中 name 词元，`github` 命中上游名（P1）。`Covered` 最高排第一 | 表驱动用例，断言首位 |
| `twitter timeline` 召回 `reach_twitter_user_timeline` | snake_case 拆词后两个词元均命中 name | 表驱动用例 |
| 多上游同名按标签区分 | **前提修正**：同名已归并。改为断言结果携带 `upstream` 与 `sourceCount`，且相似不同名工具（`vm_list` / `container_list`）能通过上游名词元区分排序 | P1 表驱动用例 |
| 未授权上游工具不可见 | `search_tools` 结果必须是 `BuildToolSet(apiKeyID)` 的子集 | 属性测试，见 §8.2 |
| 词法检索延迟 | 缓存命中后端到端 < 20ms | 基准测试，见 §8.3 |

### 8.2 属性测试

三条，都用现有 `pgregory.net/rapid`：

1. **Property 11 重写**（§6）：命中关系 + 排序严格确定性 + 数量上限 + 零结果不报错。
2. **检索结果是可见集合的子集**：对任意可见工具集合与任意查询，`search_tools` 返回的每个 `name` 必须存在于 `BuildToolSet` 的返回中。这条锁住 §2.3 的权限不变量，防止后续为了性能直接读缓存绕过管线。
3. **分词幂等与稳定**：`Tokenize(Tokenize(s) 拼接)` 的词元集合等于 `Tokenize(s)`；对任意输入不 panic、不产生空词元。

### 8.3 基准测试

`internal/toolsearch` 下建 `BenchmarkBuild` 与 `BenchmarkSearch`，用生成的 500 工具文档集。

**本文中关于耗时量级的说法（"微秒级"、"百微秒级"）都是基于操作次数的估算，未实测。** 基准测试的作用正是验证或推翻这个估算。若实测发现 `Search` 进入毫秒级，需要回头检查是否有意外的 O(n·m) 展开（最可能的原因是 `matchScore` 里对 `descTokens` 做前缀匹配时遍历了全部 200 个词元，可考虑为前缀匹配单独建一张按首字符分桶的索引）。这条排查线索要留在代码注释里。

`internal/mcpapi` 下建端到端基准，分别测缓存命中与未命中，用于验证 §4.8 的结论。**如果未命中路径远超 20ms 而命中路径远低于 20ms，就证明了"瓶颈在构建不在检索"这个判断**，也说明 P1 的缓存是达成延迟基线的必要条件。

### 8.4 命令

```powershell
go test ./internal/toolsearch/... ./internal/mcpapi/... ./internal/aggregation/...
go test ./...
go test -bench=. -benchmem ./internal/toolsearch/...
```

前端只有文案改动，按仓库约定跑 `npm run build`。

## 9. 风险与取舍

| 风险 | 应对 |
|---|---|
| 分词后召回变宽，无关工具进入结果 | 覆盖率优先排序把无关项压到末尾；`limit` 截断天然过滤长尾。同义词展开打 0.6 折扣防止劫持排序 |
| 排序变化让依赖固定顺序的客户端行为改变 | `search_tools` 本来就没有承诺顺序（当前顺序是管线副产物），新排序是严格改进。`list_tools` 顺序完全不变 |
| 同义词表维护成本 | 上限 60 条、只覆盖高频运维术语、不做配置化。用户扩展走别名规则 |
| 缓存导致工具变更延迟可见 | 正常工具同步、上游配置、可见性规则、风险评级与备份导入均立即失效；5 秒 TTL 仅覆盖绕过正常写入边界的异常路径 |
| `upstreamConfigs` 副作用因缓存而陈旧（§4.8） | 5 秒 TTL 内可接受；在实现处显式注释此约束，禁止无分析地加长 TTL |
| 中文查询效果仍不理想 | 方案内明确为有限支持，不承诺语义搜索。文档引导用户用别名规则改中文描述 |
| Property 11 修改削弱测试强度 | 新属性额外断言了排序确定性，强度不降反升 |
| 反向前缀（§4.3）带来过度召回，`vms` 命中一堆 `v*` 工具 | 已用 `minPrefixLen`（3）与 0.7 折扣双重约束；且覆盖率优先排序会把只靠反向前缀命中的项压后。若实测噪声仍高，可把反向前缀限制为「长度差 <= 2」（即只覆盖复数与短后缀），这是一行改动 |
| 停用词表误杀工具名词元 | 已定 §4.2 的准入规则与扫描校验方法；表本身极小且不可配置，出问题时排查面很窄 |
| CJK bigram 产生噪声词元（"虚拟机"的 `拟机` 无意义） | bigram 只做相等匹配、不做前缀匹配；且中文术语主要靠短语表在分词前解决，bigram 只是兜底。噪声 bigram 极少能与英文工具名的词元相等，实际影响接近零 |
| 短语替换的子串语义误伤（键出现在无关上下文中） | 表内键都是领域术语，规模受控。若出现误伤，把该键从短语表移到词元表即可（代价是失去连写支持） |
| 批量 `get_tool` 被 LLM 滥用打满上下文 | `names` 固定上限 20；超限请求直接返回字段级校验错误且不执行部分请求，避免大响应与语义不确定 |
| 词法检索的固有局限：多概念查询的第一名可能不是用户真正想要的 | 例如查 `list vms on pve node`，`node_list` 可能因命中 `node` 而排在 `vm_list` 之前。这是词法方案无法根治的，不做特殊处理。缓解在于两者都会出现在前几名，LLM 能自行选择；这也是**不承诺 Recall@1，只承诺 Recall@K** 的原因。若这类场景成为主要抱怨来源，正确的下一步是给工具加别名让名称更贴近用户表述，而不是加权重 hack |

## 10. 与原 RFC 的差异汇总

保留原 RFC 的问题诊断与目标，替换实现路径：

- **保留**：查询归一化与分词、多词 OR 召回、字段加权、缩写同义词映射、零结果引导、稳定分页、权限闭环要求。
- **替换**：BM25 换成字段加权 + 覆盖率分层排序（更简单、可解释、无全局统计量）。
- **删除**：Embedding / 向量 / 混合检索、LLM 查询改写。理由是与"确定性低延迟"目标冲突且规模不匹配。
- **修正**：权限闭环已成立，只需补回归测试；"多上游同名"前提不成立，消歧改用字段携带；性能瓶颈在聚合集构建而非检索算法。
- **新增**：`list_tools` 上游概览与过滤、搜索结果携带 `upstream` / `sourceCount` / `schemaConflict`、描述截断、`get_tool` 批量、网关工具描述文案优化。这五项对 Smart 模式实际体验的影响大于检索算法本身，其中前三项直接决定大工具集场景下 Smart 模式是否真的省了上下文。
- **补充**：空查询语义的显式决策（§4.9）、按写入边界即时缓存失效与 TTL 兜底（§4.8）、实施者交付清单与八处高频陷阱（§11）。这些不是方案内容，而是让方案可被准确实施的必要约束。

## 11. 实施者交付清单

本节面向实际写代码的人。按顺序执行，每步都有明确产出与验证方式。**不要跳步**，尤其不要在检索内核未通过测试前就去改 `mcpapi`。

### 阶段一步骤

**步骤 1：检索内核骨架与分词**

产出：`internal/toolsearch/{doc.go,tokenize.go,stopword.go}` 与 `tokenize_test.go`。

验收：
- `Tokenize("reach_twitter_user_timeline")` == `[reach twitter user timeline]`
- `Tokenize("userTimeline")` == `[user timeline]`
- `Tokenize("HTTPServer")` == `[http server]`
- `Tokenize("vm100")` == `[vm 100]`
- `Tokenize("虚拟机")` == `[虚 拟 机 虚拟 拟机]`（顺序：先单字后 bigram，实现里固定一种并写进测试）
- `Tokenize("")` 与 `Tokenize("  ___  ")` 都返回空切片，不是 nil panic
- 命令：`go test ./internal/toolsearch/...`

**步骤 2：同义词两张表与展开**

产出：`internal/toolsearch/synonym.go` 与测试。

验收：
- `applyPhraseSynonyms("create pull request")` 结果含 `pr`
- `applyPhraseSynonyms("列出虚拟机")` 结果含 `vm` 与 `qemu`（验证中文子串替换生效，这是最容易写错的一处）
- `tokenSynonyms["pr"]` == `[pull request]`
- 短语替换按键长度降序：含 `virtual machine` 的查询不会被更短的键先截走

**步骤 3：打分、排序与分页**

产出：`internal/toolsearch/index.go`，`Build` / `Search` 可用。

验收（表驱动，文档集固定写在测试里）：
- 查 `github create pr` → `create_pull_request` 排第一
- 查 `twitter timeline` → `reach_twitter_user_timeline` 排第一
- 查 `list vms` → `vm_list` 能召回（验证反向前缀）
- 查 `create_pull_request` → 该工具排第一（验证整串加成）
- 查 `帮我在 github 上创建一个 pr` → `create_pull_request` 排第一（验证停用词过滤）
- 同 `Covered` 同 `Score` 的两个工具，顺序按名称长度再字典序，重复调用结果一致
- `Search(q, 10, 0)` 与 `Search(q, 10, 10)` 拼接后无重复无遗漏，`Total` 一致
- `offset` 大于 `Total` 时返回空 `Hits` 且 `Total` 正确

**步骤 4：兜底与建议**

产出：`internal/toolsearch/suggest.go`，`Result.Fallback` 与 `Suggestions` 可用。

验收：
- 词元零召回但子串能命中时，`Fallback == true` 且有结果
- 完全无命中时 `Total == 0` 且 `Suggestions` 非空、重复调用完全一致
- `Suggestions` 长度不超过 3

**步骤 5：属性测试与基准**

产出：`index_property_test.go`、`bench_test.go`。三条属性见 §8.2。

验收：`go test ./internal/toolsearch/... -run Property` 与 `go test -bench=. -benchmem ./internal/toolsearch/...` 均通过并记录数据。

**步骤 6：接入 `mcpapi`**

产出：`smartmode.go` 与 `service.go` 按 §7 阶段一改完，8+1+1 处测试调用点同步修改。

验收：
- `go build ./...` 通过
- `go test ./internal/mcpapi/...` 通过（含重写后的 Property 11）
- 手工确认 `search_tools` 的 JSON 出参含 `tools` / `nextCursor` / `suggestions` / `hint`，且 `tools[i]` 含 `sourceCount`

**步骤 7：spec 同步与全量回归**

产出：`requirements.md`、`design.md`、`tasks.md` 三处按 §6 改完。

验收：`go test ./...`；`git diff --check` 无空白错误。

### 阶段二步骤

**步骤 8：短缓存与失效装饰器**

先做缓存，再做 `BuildToolDetails` 切换，顺序不可颠倒（原因见 §7）。

产出：`internal/aggregation/cache.go`、`Service` 内的缓存字段与 `InvalidateToolSetCache`、`build.go` 装配。

验收：
- 新增测试：连续两次 `BuildToolSet` 只触发一次 `ListUpstreams`（用计数 fake）
- 新增测试：`NewInvalidatingCache` 包装后调用 `Replace` 成功，随后 `BuildToolSet` 重新构建
- 新增测试：`Replace` 返回错误时**不**失效缓存
- 新增测试：成功的工具集失效会同步清理可能绑定旧来源的工具结果缓存；失败写入不清理
- 端到端基准对比缓存命中与未命中，验证 §4.8 的判断
- 代码注释里写明 `upstreamConfigs` 副作用约束（§4.8）

**步骤 9：上游信息进入检索与结果**

产出：`ToolSourceView.UpstreamTags`、`toolDetailSource` 可选接口、`ToolSummary.Upstream`、`web/src/api/tools.ts` 类型同步。

验收：
- `go test ./internal/aggregation/... ./internal/mcpapi/... ./internal/httpapi/...` 通过
- 未实现可选接口的 fake 仍能通过全部既有测试（验证退化路径）
- `npm run build` 通过

**步骤 10：`list_tools` 概览过滤与 `get_tool` 批量**

产出：§4.7 对应改动。

验收：
- `cursor == ""` 时返回 `upstreams`，翻页时不返回
- `upstream` 传上游名全等与子串两种形式都能过滤
- `upstream` 传不存在的值返回空页 + 概览，不报错
- 只传 `name` 时 `get_tool` 返回体与改动前逐字节一致（回归既有 e2e）
- 传 `names` 含 1 个可见 1 个不可见时，`tools` 长度 1、`notFound` 长度 1
- `names` 为空或超过 20 个时返回字段级校验错误，且不执行部分请求

### 全流程验证命令

```powershell
go build ./...
go test ./internal/toolsearch/... ./internal/mcpapi/... ./internal/aggregation/...
go test ./...
go test -bench=. -benchmem ./internal/toolsearch/...
git diff --check
```

前端有改动时追加：

```powershell
npm run build
```

（在 `web` 目录下执行）

### 实施中最容易做错的八处

按踩坑概率排序。每一条都会导致功能看起来能跑但实际无效，且不会有编译错误提醒你。

1. **中文同义词写进词元表而不是短语表**（§4.4）。结果是所有中文查询完全无效。判定：`applyPhraseSynonyms("虚拟机")` 必须产出英文词。
2. **短语替换时没给值补两侧空格**（§4.4）。结果是"配置文件"变成 `configfile` 这种不存在的词元，中文多词查询全部失效。判定：`applyPhraseSynonyms("配置文件")` 分词后必须是 `[config file]` 两个词元。
3. **靠同义词命中时忘记 `Covered += 1`**（§4.3）。结果是 `pr` 能匹配上但排在最后，看起来"同义词没用"。判定：查 `create pr` 时 `create_pull_request` 的 `Covered` 必须是 2。
4. **前缀匹配方向写反或只做单向**（§4.3）。结果是 `vms`、`containers` 这类复数查询召回为 0。判定：查 `list vms` 必须召回 `vm_list`。
5. **停用词表放进了工具名高频词**（§4.2）。结果是 `get help`、`show list` 这类查询被过滤成空。判定：停用词表与真实工具集的词元集交集必须为空。
6. **排序漏掉第三级（名称长度与字典序）**（§4.3）。结果是分页偶发漏项、测试偶发失败，且极难复现。判定：同分工具重复查询 100 次顺序完全一致。
7. **忘记同步 8 处 `SearchTools` 测试调用点**（§7）。编译失败会快速暴露，这反而是好事。真正危险的是只改签名不改断言语义，让测试用旧期望"侥幸"通过。
8. **P1 先切 `BuildToolDetails` 再做缓存**（§7）。结果是搜索变慢而不是变快，然后误判为"检索算法太慢"。顺序必须是先缓存后切换。

### 交付判定

以下全部满足才算完成阶段一：

- [ ] §8.1 的前两个场景（`github create pr`、`twitter timeline`）有对应表驱动测试且首位命中
- [ ] §8.2 三条属性测试通过
- [ ] `go test ./...` 全绿
- [ ] `search_tools` 零结果时返回 `suggestions` 与 `hint`
- [ ] `ToolSummary.Description` 已按 240 rune 截断
- [ ] spec 三件套（requirements / design / tasks）已同步
- [ ] 基准数据已记录，用于判断是否需要立刻做阶段二

阶段二额外满足：

- [ ] 缓存命中路径的端到端延迟 < 20ms（§8.1 的性能基线）
- [ ] 权限子集属性测试（§8.2 第 2 条）在缓存启用后仍通过
- [ ] `npm run build` 通过
- [ ] `list_tools` 首页返回上游概览，`get_tool` 支持批量
