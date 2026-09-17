# 025: バスプロトコルのバッチ化とアドレッシング是正

## 背景 (Background)

### 問題 1: コマンドが黙って破棄されている

`ChannelBus.Publish` は購読チャネルが満杯のとき **`default:` で黙って破棄** する
（`features/neurom/internal/bus/channelbus.go` 31-41 行）。

```go
for subTopic, chans := range b.subs {
	if strings.HasPrefix(topic, subTopic) {
		for _, ch := range chans {
			select {
			case ch <- msg:
			default:            // ← バッファ満杯なら破棄
			}
		}
	}
}
```

購読チャネルのバッファは **100**（同ファイル 45 行）。

これに対し `internal/modules/cpu/cpu.go` の `scene2Update`（278-296 行）は
8×8 タイルで画面全体を敷き詰めるため、**1 tick あたり 32 × 27 = 864 回 `Publish`** する。

```go
for ty := 0; ty < VRAMHeight; ty += 8 {
	for tx := 0; tx < VRAMWidth; tx += 8 {
		c.publishBlitRect(b, 0, uint16(tx), uint16(ty), 8, 8, 0, pixels)
	}
}
```

tick 間隔は 8ms（`TickInterval`）。バッファ 100 に対して 864 投入なので、
**現状すでに大半の描画コマンドが破棄されている**。
これは拡張ソフトウェアを TCP 越しに動かせばさらに悪化する。

### 問題 2: イベント側も同じ量で溢れる

VRAM は 1 コマンドごとに結果イベントを発行する。`blit_rect` なら `rect_updated`
（`vram.go` 738-747 行）。したがって上記シーンでは **イベントも 864 件/tick** 流れる。

Monitor 側の受け口も同じくバッファ 100 の購読チャネルであり、
`handleVRAMEvent`（`monitor.go` 243-301 行）は `rect_updated` を受けても
`markDirty()` するだけで、複数イベントを 1 回の再描画にまとめている。

```go
case "rect_updated", "rect_copied", "page_size_changed", "viewport_changed",
	"palette_block_updated":
	m.mu.Lock()
	m.markDirty()
	m.mu.Unlock()
```

つまり **864 件のイベントのうち意味があるのは 1 件だけ**で、残りは純粋な無駄である。
さらに悪いことに、この洪水によって Monitor のチャネルが溢れると、
`display_page_changed` や `palette_block_updated` のような
**状態変化イベントが取りこぼされ、表示が壊れる**可能性がある。

### 問題 3: トピックの前方一致で VRAM が自己配信している

`ChannelBus` の購読判定は `strings.HasPrefix(topic, subTopic)`。

- VRAM は `Subscribe("vram")`（`vram.go` 78 行）
- VRAM は結果イベントを `Publish("vram_update", ...)`（同 322 行等）
- `strings.HasPrefix("vram_update", "vram")` == **true**

したがって **VRAM は自分が発行したイベントを自分で受信し直している**。

`handleMessage`（`vram.go` 150-204 行）はイベント名が `switch` のどのケースにも
当たらないため実処理は行わないが、その手前の 159-160 行で**無条件に統計を記録**している。

```go
start := hrNow()
defer func() { v.stats.Record(msg.Target, hrSince(start)) }()
```

イベントは `Operation: bus.OpCommand` で発行されているため 151 行のガードも通過する。
結果、`/stats` に `rect_updated` / `vram_cleared` / `palette_block_updated` といった
**イベント名がコマンドとして混入**し、ミューテックス取得と goroutine 往復のコストも無駄に発生する。

### 問題 4: 読み出しコマンドに相関 ID がない

`read_rect`（`vram.go` 465-499 行）と `read_palette_block`（585-609 行）は、
結果を `vram_update` トピックへ `Target: "rect_data"` / `"palette_data"` として
**一方的に publish** する。

リクエストとレスポンスを対応付ける ID がないため、複数の読み出しが並行すると
どの応答が自分のものか判別できない。`BusMessage` に `Source` はあるが、
レスポンスの `Source` は常に `"VRAM"` で上書きされるため識別に使えない。
SDK が同期的な `ReadRect()` を提供するには相関 ID が必須である。

### 本仕様の位置づけ

拡張ソフトウェア分離（024〜029）の第 2 ステップ。
**基盤側のバスプロトコルのセマンティクスを、外部クライアントに公開できる品質へ引き上げる。**

- 前提: [024-ARCProtocol-PackageExtraction](file://prompts/phases/000-foundation/branches/main/ideas/024-ARCProtocol-PackageExtraction.md)（`arcproto` パッケージ）
- 後続: 027（SDK が `batch` と相関 ID を利用）、028（外部トランスポート）

## 要件 (Requirements)

### 必須要件

1. **R1: `batch` コマンドの新設**
   - 複数の VRAM コマンドを 1 メッセージで受け取る Target `batch` を追加する。
   - payload レイアウト:
     ```
     [count:u16]
     エントリ × count:
       [command_id:u8][payload_len:u32][payload... : payload_len bytes]
     ```
   - `payload_len` は `u32`。単一コマンドの payload が 512×512 ページの全面転送で
     262,154 バイトに達し `u16`（最大 65,535）では表現できないため。
   - VRAM は **1 回のミューテックス取得の中で全エントリを順次適用**する
     （フレーム内ティアリングの防止）。
   - エントリ内に `batch` 自身を含めることは禁止（ネスト不可）。検出時はそのエントリをスキップする。

2. **R2: 数値コマンド ID の導入**
   - `arcproto` に各コマンドの `uint8` 数値 ID を定義する。
   - `batch` エントリ内では文字列 Target ではなく数値 ID を使う（サイズと解析コストの削減）。
   - 数値 ID と文字列 Target の相互変換関数を `arcproto` に置く。
   - **数値 ID は永久に不変**とする（プロトコル互換性のため）。新コマンドは末尾に追加する。

3. **R3: バッチ内イベントの合流**
   - バッチ適用中、**描画系コマンドの個別イベントを抑止**する。
     対象: `vram_updated`, `vram_cleared`, `rect_updated`, `rect_copied`
   - バッチ完了時に、抑止した描画範囲の **和集合（bounding box）を 1 件の `rect_updated`**
     として発行する。
   - **状態変化系イベントは抑止しない**（Monitor が状態を追跡するために必要であり、かつ
     1 フレームあたり数件しか発生しないため洪水にならない）。
     対象: `palette_updated`, `palette_block_updated`, `display_page_changed`,
     `page_count_changed`, `page_size_changed`, `pages_swapped`, `page_copied`,
     `viewport_changed`, `page_error`
   - 読み出し系（`rect_data`, `palette_data`）はバッチ内でも個別に発行する。

4. **R4: バッチの統計記録**
   - `batch` を 1 コマンドとして記録するのではなく、**内包する各コマンドを個別に記録**する。
   - これにより `/stats` の粒度が現状と等価に保たれ、`features/stats` CLI の出力が意味を持ち続ける。
   - 加えて `batch` 自体の実行時間とエントリ数も記録する（バッチ効果の測定用）。

5. **R5: トピックアドレッシングの是正**
   - あるトピックの購読者が、別トピックのメッセージを前方一致で受信してしまう問題を解消する。
   - 区切り文字を意識したマッチングに変更する。
     `topic == subTopic` または `strings.HasPrefix(topic, subTopic + セパレータ)` のときのみ一致とする。
   - トピック名を階層構造へ改称する（`arcproto` の定数として定義）。
     現状の `vram` / `vram_update` のように一方が他方の接頭辞になる命名を廃止する。
   - **`Subscribe("")` は全トピック一致として特別扱いする**
     （`internal/bus/tcpbridge.go` 119 行が全メッセージ購読に使用しているため、
     この挙動を壊すと外部ブリッジが停止する）。
   - 是正後、VRAM が自分のイベントを受信しないことを確認する。

6. **R6: 相関 ID の導入**
   - `bus.BusMessage` に要求 ID フィールドを追加する。
   - 読み出しコマンド（`read_rect`, `read_palette_block`, `get_stats`）に対する応答は、
     要求メッセージの ID を**そのまま複写して返す**。
   - ID を指定しない（ゼロ値）要求に対しては、現状どおり ID なしで応答する（後方互換）。
   - `gob` エンコードはフィールド追加を透過的に扱うため、028 までは既存の
     `Encode` / `Decode`（`internal/bus/message.go`）の変更は不要。

7. **R7: 破棄の可視化**
   - `ChannelBus` に、購読者ごとの破棄件数カウンタを追加する。
   - 破棄が発生した場合、**流量に対して抑制された頻度**で警告ログを出力する
     （1 件ごとにログを出すと、それ自体が新たな洪水になるため）。
   - 破棄件数を `/stats` エンドポイントから取得できるようにする。
   - **非ブロッキング（破棄）方針は維持する**。理由は「実現方針」に記載。

8. **R8: 振る舞いの互換性**
   - `batch` を使わない既存の単発コマンド経路は、これまでと同一に動作すること。
   - デモの表示結果が変わらないこと（本仕様ではデモを `batch` に書き換えない。027 で行う）。
   - 既存の統合テストが無変更で PASS すること（トピック名改称に伴う参照修正は除く）。

### 任意要件

9. **R9: バッチのサイズ上限**
   - 1 バッチあたりの最大エントリ数・最大合計バイト数の上限を定義し、超過時は
     `page_error` 相当のエラーイベントを発行する。外部クライアントからの
     メモリ枯渇攻撃への備え（本格的な検証は 028 で扱う）。

10. **R10: バッチ効果のベンチマーク**
    - `scene2Update` 相当の 864 コマンドを、単発 864 メッセージと 1 バッチで
      それぞれ実行し、破棄件数と所要時間を比較するベンチマークを追加する。
    - `internal/modules/vram/parallel_bench_test.go` に既存のベンチマーク基盤がある。

## 実現方針 (Implementation Approach)

### バッチ payload の構造

```
オフセット  内容
0           count             : u16   エントリ数
2           エントリ 0
...
エントリ = [command_id : u8][payload_len : u32][payload : payload_len bytes]
```

`arcproto` 側の実装イメージ:

```go
package arcproto

type CommandID uint8

const (
	CmdMode             CommandID = 1
	CmdDrawPixel        CommandID = 2
	CmdSetPalette       CommandID = 3
	CmdSetPaletteBlock  CommandID = 4
	CmdClearVRAM        CommandID = 5
	CmdBlitRect         CommandID = 6
	CmdBlitRectTransform CommandID = 7
	CmdCopyRect         CommandID = 8
	// ... 以降 append only。既存値の変更は禁止
)

// Batch は複数コマンドを 1 メッセージにまとめる。
type Batch struct {
	entries []batchEntry
}

func (b *Batch) Add(id CommandID, payload []byte) { ... }
func (b *Batch) Len() int                          { ... }
func (b *Batch) Encode() []byte                    { ... }

// BatchIter は確保を伴わずにエントリを走査する。
type BatchIter struct { ... }

func NewBatchIter(data []byte) (*BatchIter, error)
func (it *BatchIter) Next() (CommandID, []byte, bool)
```

デコードは**イテレータ方式**にして、エントリごとのスライス確保を避ける
（`payload` は元データのサブスライスを返す）。

### VRAM 側のバッチ適用

現状 `handleMessage` は `v.mu.Lock()` を取ってから `switch` に入り、
各 `handle*` はロック保持を前提としている（`vram.go` 162-204 行）。
この構造をそのまま活かせる。

```go
func (v *VRAMModule) handleBatch(msg *bus.BusMessage) {
	it, err := arcproto.NewBatchIter(msg.Data)
	if err != nil {
		return
	}

	// 描画イベントを抑止し、範囲を蓄積するモードに入る
	v.beginCoalesce()
	defer v.endCoalesce()   // 和集合を 1 件の rect_updated として発行

	for {
		id, payload, ok := it.Next()
		if !ok {
			break
		}
		if id == arcproto.CmdBatch {
			continue // ネスト禁止
		}
		start := hrNow()
		v.applyCommand(id, payload, msg)
		v.stats.Record(arcproto.TargetOf(id), hrSince(start))   // R4: 個別に記録
	}
}
```

`applyCommand` は現在の `switch msg.Target` を数値 ID ベースに分岐させた共通関数とし、
単発経路（`handleMessage`）とバッチ経路の両方から呼ぶ。
これにより**適用ロジックが二重化しない**。

### イベント合流の実装

```go
type coalesceState struct {
	active  bool
	dirty   bool
	x0, y0  int
	x1, y1  int   // 排他的上限
}

// publishRectEvent は合流中なら発行せず範囲を広げるだけにする。
func (v *VRAMModule) publishRectEvent(x, y, w, h int) {
	if v.coalesce.active {
		v.coalesce.expand(x, y, w, h)
		return
	}
	// 従来どおり即時発行
}
```

`clear_vram` はページ全面、`copy_rect` は転送先矩形として範囲に加える。
`endCoalesce` で `dirty` が立っていれば 1 件だけ `rect_updated` を発行する。

なお Monitor は `rect_updated` の payload の矩形値を実際には使わず
`markDirty()` するだけ（`monitor.go` 287-291 行）なので、
和集合の精度は現時点の描画正しさには影響しない。
ただし将来の部分更新最適化のために正しい範囲を入れておく。

### トピック階層の改称案

| 現在 | 改称後 | 用途 |
|---|---|---|
| `vram` | `cmd.vram` | VRAM へのコマンド |
| `vram_update` | `evt.vram` | VRAM からのイベント |
| `monitor` | `cmd.monitor` | Monitor へのコマンド |
| `monitor_update` | `evt.monitor` | Monitor からのイベント |
| `system` | `sys` | システム制御 |

セパレータを `.` とする。マッチング規則:

```go
func topicMatches(topic, subTopic string) bool {
	if subTopic == "" {
		return true   // R5: 全購読（TCPBridge が使用）
	}
	if topic == subTopic {
		return true
	}
	return strings.HasPrefix(topic, subTopic+".")
}
```

これにより `cmd.vram` の購読者が `evt.vram` を受け取ることはなくなり、
`cmd` の購読で全コマンドを傍受するという階層的な使い方も可能になる。

改称は `arcproto` の定数を通して行うため、参照箇所の修正は機械的に完了する。

### 破棄方針を「非ブロッキング維持」とする理由

ブロッキング化には**自己デッドロックの危険**がある。
VRAM は `v.mu` を保持したままイベントを `Publish` する（`vram.go` 322 行等）。
現状は前方一致により VRAM 自身が購読者に含まれる（問題 3）ため、
仮に `Publish` をブロッキングにすると **VRAM が自分のチャネル満杯を自分で待ち続ける**
という完全なデッドロックが成立する。

R5（自己配信の解消）が入れば直接の自己デッドロックは消えるが、
`VRAM → Monitor → system` のような循環経路が将来生まれた場合に同種の問題が再発する。
したがって本仕様では **非ブロッキング（破棄）を意図的な設計として確定させ、
その代わりに破棄を可視化する**（R7）方針を採る。

破棄が起きないことは、バッチ化（R1）によって流量そのものを
864 件/tick → 1 件/tick に削減することで達成する。

### 変更対象ファイル

| ファイル | 変更内容 |
|---|---|
| `features/neurom/arcproto/` | `CommandID`、`Batch`、`BatchIter`、トピック定数の改称 |
| `features/neurom/internal/bus/channelbus.go` | `topicMatches` 導入、破棄カウンタ |
| `features/neurom/internal/bus/message.go` | 要求 ID フィールド追加 |
| `features/neurom/internal/modules/vram/vram.go` | `handleBatch`、`applyCommand` 共通化、イベント合流、統計の個別記録、応答への ID 複写 |
| `features/neurom/internal/modules/monitor/monitor.go` | トピック定数参照への置換 |
| `features/neurom/internal/modules/cpu/cpu.go` | トピック定数参照への置換 |
| `features/neurom/internal/modules/io/io.go` | トピック定数参照への置換 |
| `features/neurom/internal/statsserver/server.go` | 破棄件数の出力追加 |
| `features/neurom/integration/*_test.go` | トピック名の参照修正、バッチの統合テスト追加 |

### 本仕様で扱わないこと（スコープ外）

- デモを `batch` で書き換えること → 027
- 外部 TCP の双方向化、エンベロープのバイナリ化 → 028
- 32KB チャンク分割の配線 → 028
  （`internal/bus/chunk.go` は実装済みだが**どのトランスポートからも呼ばれていない死んだコード**であり、
  `EncodeChunks` の呼び出し元は `chunk_test.go` のみ。in-process の `ChannelBus` は
  ポインタを渡すだけなので分割不要であり、必要になるのは TCP 経路である）
- 入力・vsync → 026

## 検証シナリオ (Verification Scenarios)

1. `arcproto` に `CommandID` 定数、`Batch` ビルダ、`BatchIter` を追加し、単体テストを通す
2. `arcproto` のトピック定数を階層命名（`cmd.vram` / `evt.vram` / `sys` 等）へ改称する
3. `ChannelBus` に `topicMatches` を導入し、`Subscribe("")` が全一致を維持することを単体テストで確認する
4. `ChannelBus` に破棄カウンタと抑制付き警告ログを追加する
5. 各モジュール（vram / cpu / monitor / io）のトピック参照を `arcproto` 定数へ置換する
6. VRAM が自分の発行したイベントを受信していないことを確認する（統計に `rect_updated` 等が現れないこと）
7. `vram.go` の `switch msg.Target` を数値 ID ベースの `applyCommand` に共通化し、単発経路が従来と同一に動くことを確認する
8. `handleBatch` とイベント合流（`beginCoalesce` / `endCoalesce`）を実装する
9. バッチで 864 件の `blit_rect` を投入し、(a) 破棄が 0 件であること (b) 発行される `rect_updated` が 1 件であること (c) 描画結果が単発 864 件の場合と同一であることを統合テストで確認する
10. `BusMessage` に要求 ID を追加し、`read_rect` の応答に同じ ID が複写されることを統合テストで確認する
11. 並行して 2 件の `read_rect` を異なる ID で発行し、各応答が正しく識別できることを確認する
12. `/stats` を取得し、バッチ内の各コマンドが個別に計上されていること、および破棄件数が出力されていることを確認する
13. `scripts/process/build.sh` を実行し、全ビルドと単体テストが PASS することを確認する
14. `./bin/neurom.exe` を起動し、デモ 7 シーンの表示が本仕様の変更前と同じであることを目視確認する

## テスト項目 (Testing for the Requirements)

| 要件 | 検証方法 | コマンド / 手段 |
|---|---|---|
| R1: `batch` コマンド | バッチの Encode / Decode ラウンドトリップ PASS | `cd features/neurom && go test -v -count=1 -run "TestBatch" ./arcproto/...` |
| R1: 単一ロック適用 | バッチ適用中に他コマンドが割り込まないことを確認 | `cd features/neurom && go test -v -count=1 -run "TestBatchAtomicity" ./integration/...` |
| R2: 数値コマンド ID | ID ↔ Target 相互変換の全網羅テスト PASS | `cd features/neurom && go test -v -count=1 -run "TestCommandID" ./arcproto/...` |
| R3: イベント合流 | 864 件バッチで `rect_updated` が 1 件のみ発行される | `cd features/neurom && go test -v -count=1 -run "TestBatchEventCoalesce" ./integration/...` |
| R3: 状態イベント維持 | バッチ内の `set_display_page` が `display_page_changed` を発行する | `cd features/neurom && go test -v -count=1 -run "TestBatchStateEvent" ./integration/...` |
| R4: 統計の粒度 | `/stats` にバッチ内の個別コマンド名が現れる | `./bin/neurom.exe --headless --stats-port 8080` + `cd features/stats && go run . --endpoint http://127.0.0.1:8080/stats` |
| R5: アドレッシング是正 | `cmd.vram` 購読者が `evt.vram` を受信しない | `cd features/neurom && go test -v -count=1 -run "TestTopicMatches" ./internal/bus/...` |
| R5: 全購読の維持 | `Subscribe("")` が全トピックを受信する | 同上テスト内で検証 |
| R5: 自己配信の解消 | `/stats` に `rect_updated` 等のイベント名が現れない | `/stats` 出力の目視確認、および `TestStatsNoEventNames` |
| R6: 相関 ID | `read_rect` の応答に要求 ID が複写される | `cd features/neurom && go test -v -count=1 -run "TestRequestIDCorrelation" ./integration/...` |
| R6: 並行読み出しの識別 | 異なる ID の 2 要求が正しく対応付く | 同上テスト内で検証 |
| R6: 後方互換 | ID ゼロ値の要求が従来どおり応答される | 既存の `read_rect` 統合テストが無変更で PASS |
| R7: 破棄の可視化 | 意図的に溢れさせて破棄カウンタが増加する | `cd features/neurom && go test -v -count=1 -run "TestDropCounter" ./internal/bus/...` |
| R7: ログ抑制 | 大量破棄でもログ件数が上限内に収まる | 同上テスト内で検証 |
| R8: 単発経路の互換 | 既存の VRAM 統合テストが全件 PASS | `scripts/process/integration_test.sh --specify "TestPageManagementIntegration\|TestPageDrawIsolationIntegration\|TestVRAMEnhancement\|TestPaletteUpdate"` |
| R8: デモの表示不変 | デモ 7 シーンが変更前と同じ | `./bin/neurom.exe` を 21 秒以上起動して目視 |
| R9: サイズ上限 | 上限超過バッチでエラーイベントが発行される | `cd features/neurom && go test -v -count=1 -run "TestBatchLimit" ./integration/...` |
| R10: バッチ効果 | 単発 864 件と 1 バッチの破棄件数・所要時間を比較 | `cd features/neurom && go test -bench "BenchmarkBatch" -run "^$" ./internal/modules/vram/...` |
| 全体リグレッション | ビルド + 全単体テスト | `scripts/process/build.sh` |

### 補足: 検証スクリプトの現状

`scripts/process/build.sh` は各 feature で `go list ./... | grep -v '/tests/'` を実行するため、
`features/neurom/integration/` も単体テストとして実行される。実質的な全体検証ゲートは `build.sh` である。
`scripts/process/integration_test.sh` はリポジトリルートの `tests/go.mod` を前提とするが、
現状 `tests/` は存在しないため warn を出して exit 0 する（no-op）。
上表の `integration_test.sh --specify` は統合テストが `tests/` へ移設された後に有効となり、
それまでは `build.sh` および `cd features/neurom && go test -run ... ./integration/...` で代替する。


> [!NOTE]
> `--categories` および階層分離は仕様 030 実装後に使用可能。それ以前は `integration_test.sh` が無効で、統合テストは `build.sh` 側で実行されていた。
## 対応ステータス

- **ステータス**: 未着手
- **実装計画**: 未作成
