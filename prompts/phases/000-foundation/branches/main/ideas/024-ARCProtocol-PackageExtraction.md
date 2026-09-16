# 024: ARC プロトコルパッケージの抽出と仕様書化

## 背景 (Background)

### 現状の問題

拡張ソフトウェア（Metov 等）を基盤（Neurom）から分離するにあたり、両者が共有すべき「契約」＝
バスコマンドのバイトレイアウト定義が、コードベース内に **3 箇所重複** している。

| 箇所 | 役割 | 行数目安 |
|---|---|---|
| `features/neurom/internal/modules/cpu/cpu.go` の `publish*` 系 12 関数 | エンコード（送信側） | 約 125 行 |
| `features/neurom/internal/modules/vram/vram.go` の `handle*` 系 | デコード（受信側） | 約 400 行 |
| `features/neurom/integration/*_test.go` のローカルヘルパー | エンコード（テスト用） | 散在 |

エンコード側の典型例（`cpu.go` 130-143 行）:

```go
func (c *CPUModule) publishBlitRect(b bus.Bus, page uint8, x, y, w, h uint16, blendMode uint8, pixels []uint8) {
	data := make([]byte, 10+len(pixels))
	data[0] = page
	binary.BigEndian.PutUint16(data[1:], x)
	binary.BigEndian.PutUint16(data[3:], y)
	binary.BigEndian.PutUint16(data[5:], w)
	binary.BigEndian.PutUint16(data[7:], h)
	data[9] = blendMode
	copy(data[10:], pixels)
	b.Publish("vram", &bus.BusMessage{
		Target: "blit_rect", Operation: bus.OpCommand,
		Data: data, Source: c.Name(),
	})
}
```

これと対応するデコード側（`vram.go` 329-345 行）は、同じオフセット（1, 3, 5, 7, 9, 10）を
**独立に手書き**している。片方だけを変更すると静かに壊れる構造であり、
プロトコルを第三者に公開する前提では許容できない。

さらに `integration/vram_page_test.go` 71-79 行では、テストが自前で `read_rect` のバイト列を組み立てている。

```go
readData := make([]byte, 9)
readData[0] = 0
binary.BigEndian.PutUint16(readData[1:], 0)
// ... 以下手書き
```

### 派生する 2 つの問題

1. **プロトコル仕様書が存在しない**
   `prompts/specifications/VRAM-Specification.md` は以下 3 行のプレースホルダのままである。

   ```
   # VRAMの仮想ハードウェア仕様: 機能説明とコマンド一覧

   **To Be Described**
   ```

   コマンド一覧は `ideas/012-VRAMPage.md` や `ideas/016-VRAMFeatureReintegration.md` に断片的に
   散在しているのみで、しかも実装と乖離している箇所がある
   （例: `012` の `blit_rect` は `[src_page][dst_page]` の 2 ページ指定だが、実装は `[page]` の 1 個のみ）。
   **信頼できる一覧は現在の実装コードしかない。**

2. **`internal/` により外部から到達不可能**
   `features/neurom/go.mod` のモジュールパスは `github.com/axsh/neurom`。
   バス・プロトコル・モジュールすべてが `features/neurom/internal/` 配下にあるため、
   Go の `internal` 可視性ルールにより `github.com/axsh/neurom` 以外のモジュールから import できない。
   拡張ソフトウェアを別 `go.mod`（別バイナリ）にした瞬間、コードを一切共有できなくなる。

### 本仕様の位置づけ

拡張ソフトウェア分離（024〜029）の**最初のステップ**であり、
**外部通信・振る舞いを一切変更しない純粋なリファクタリング**である。
後続仕様がすべてこのパッケージの上に乗る。

| 仕様 | 内容 | 依存 |
|---|---|---|
| **024（本仕様）** | `arcproto` 抽出 + プロトコル仕様書 | なし |
| 025 | バッチコマンド / 相関 ID / トピック整理 | 024 |
| 026 | 入力とフレームクロック | 024 |
| 027 | `arc` SDK と in-process API | 024, 025, 026 |
| 028 | 外部バスの双方向化 | 024, 025 |
| 029 | Metov の別バイナリ分離 | 027, 028 |

## 要件 (Requirements)

### 必須要件

1. **R1: `arcproto` パッケージの新設**
   - `features/neurom/arcproto/` を作成する（**`internal/` の外**であることが本質的に重要）。
   - モジュール外から `github.com/axsh/neurom/arcproto` として import 可能であること。
   - このパッケージは `internal/bus` を **import してはならない**（プロトコル定義がトランスポートに依存しない）。
     バイト列の生成・解析のみを責務とし、`Publish` は行わない。

2. **R2: コマンド・イベント・トピックの定数化**
   - VRAM の全 17 コマンドの Target 文字列を定数として定義する。
   - VRAM が発行する全イベントの Target 文字列を定数として定義する。
   - トピック名（`vram`, `vram_update`, `system`, `monitor`, `monitor_update`）を定数として定義する。
   - 現在コード中に文字列リテラルで散在している箇所をすべて定数参照に置き換える。

3. **R3: コマンドごとの型と Encode / Decode の対称実装**
   - 各コマンドに対応する構造体を定義し、`Encode() []byte` と `DecodeXxx([]byte) (Xxx, error)` を提供する。
   - **エンコードとデコードが同一ファイル内で隣接**していること（drift の構造的防止）。
   - 対象コマンド: `mode`, `draw_pixel`, `set_palette`, `set_palette_block`, `read_palette_block`,
     `clear_vram`, `blit_rect`, `blit_rect_transform`, `read_rect`, `copy_rect`,
     `set_page_count`, `set_display_page`, `swap_pages`, `copy_page`, `set_page_size`,
     `set_viewport`, `get_stats`
   - 対象イベント（レスポンス payload を持つもの）: `rect_data`, `palette_data`, `rect_updated`, `page_error`

4. **R4: 単位型の導入**
   - 回転を表す型を定義し、度・回転数からの変換関数を提供する（現状は 0-255 の生 `uint8`）。
   - 拡大率を表す 8.8 固定小数点型を定義し、`float64` からの変換関数を提供する
     （現状は `0x0100` = 等倍という魔法数が `cpu.go` 333-337 行に直書き）。
   - ブレンドモードを型付き定数として定義する（現状は `uint8(mode)` の生値）。
   - パレット色を表す型を定義する（現状は `[4]uint8` の生配列）。

5. **R5: 既存呼び出し元の置き換え**
   - `internal/modules/vram/vram.go` の `handle*` が `arcproto` の Decode を使用すること。
   - `internal/modules/cpu/cpu.go` の `publish*` が `arcproto` の Encode を使用すること
     （**デモシーン本体は本仕様では移動させない**。027 / 029 で扱う）。
   - `integration/*_test.go` のローカルエンコードヘルパーを `arcproto` に置き換えること。

6. **R6: 振る舞いの完全な不変性**
   - 生成されるバイト列が **1 ビットも変わらない**こと。
   - バス上を流れるメッセージの Target / Operation / Source / Data がすべて同一であること。
   - デモの表示結果が変わらないこと。
   - 不正長 payload に対する挙動を維持すること
     （現状は `vram.go` の各 `handle*` が長さ不足時に**黙って `return`** する。
     `arcproto` の Decode は `error` を返すが、VRAM 側はそれを受けて同様に `return` する）。

7. **R7: プロトコル仕様書の作成**
   - `prompts/specifications/VRAM-Specification.md` のスタブを実装ベースの内容で埋める。
   - 記載必須項目: トピック一覧 / コマンド一覧（Target・payload バイトレイアウト・境界条件）/
     発行イベント一覧 / エラーコード / 座標系とクリッピング規則 / エンディアン規定 / 固定小数点の規定。
   - **実装が唯一の真実である現状を解消し、この文書を契約書とする**ことを明記する。

### 任意要件

8. **R8: ゴールデンバイト列テスト**
   - 各コマンドについて、既知の入力から既知のバイト列が生成されることを固定値で検証するテストを追加する。
   - R6 の「1 ビットも変わらない」を機械的に保証する手段として推奨。

9. **R9: ラウンドトリップテスト**
   - `Encode` → `Decode` で元の構造体が復元されることを全コマンドで検証する。

10. **R10: 既存仕様書との齟齬の記録**
    - `ideas/012-VRAMPage.md` の `blit_rect` / `blit_rect_transform` の記述が実装と異なる点を
      新仕様書内に注記する（実装を正とする）。

## 実現方針 (Implementation Approach)

### パッケージ構成

```
features/neurom/arcproto/
  doc.go          // パッケージドキュメント。プロトコルの概要
  topic.go        // トピック定数
  target.go       // コマンド / イベントの Target 定数
  types.go        // Rotation, Scale, BlendMode, Color, PageIndex
  vram_draw.go    // draw_pixel, clear_vram, blit_rect, blit_rect_transform, copy_rect
  vram_palette.go // set_palette, set_palette_block, read_palette_block
  vram_page.go    // set_page_count, set_display_page, swap_pages, copy_page, set_page_size
  vram_view.go    // set_viewport
  vram_read.go    // read_rect, および rect_data / palette_data レスポンス
  event.go        // rect_updated, page_error 等のイベント payload
```

`internal/bus` への依存を持たないため、`arcproto` は単体でテスト可能。

### 型設計

```go
package arcproto

// --- 単位型 ---

// Rotation は 0-255 で一周を表す回転量。
type Rotation uint8

func Degrees(deg float64) Rotation  // 90.0 -> 64
func Turns(t float64) Rotation      // 0.25 -> 64

// Scale は 8.8 固定小数点の拡大率。0x0100 が等倍。
type Scale uint16

const ScaleOne Scale = 0x0100

func ScaleOf(f float64) Scale       // 1.0 -> 0x0100, 2.0 -> 0x0200

// BlendMode は合成モード。値は既存の internal/modules/vram/blend.go と一致させる。
type BlendMode uint8

const (
	BlendReplace BlendMode = 0
	// 以降は blend.go の定義に合わせて列挙する
)

// Color は RGBA パレット色。
type Color struct{ R, G, B, A uint8 }
```

### コマンド型の実装パターン

```go
// BlitRect は blit_rect コマンドの payload。
// レイアウト: [page:u8][x:u16][y:u16][w:u16][h:u16][blend:u8][pixels...]
type BlitRect struct {
	Page   uint8
	X, Y   uint16
	W, H   uint16
	Blend  BlendMode
	Pixels []uint8
}

const blitRectHeaderLen = 10

func (c BlitRect) Target() string { return TargetBlitRect }

func (c BlitRect) Encode() []byte {
	data := make([]byte, blitRectHeaderLen+len(c.Pixels))
	data[0] = c.Page
	binary.BigEndian.PutUint16(data[1:], c.X)
	binary.BigEndian.PutUint16(data[3:], c.Y)
	binary.BigEndian.PutUint16(data[5:], c.W)
	binary.BigEndian.PutUint16(data[7:], c.H)
	data[9] = uint8(c.Blend)
	copy(data[blitRectHeaderLen:], c.Pixels)
	return data
}

func DecodeBlitRect(data []byte) (BlitRect, error) {
	if len(data) < blitRectHeaderLen {
		return BlitRect{}, ErrShortPayload
	}
	return BlitRect{
		Page:   data[0],
		X:      binary.BigEndian.Uint16(data[1:]),
		Y:      binary.BigEndian.Uint16(data[3:]),
		W:      binary.BigEndian.Uint16(data[5:]),
		H:      binary.BigEndian.Uint16(data[7:]),
		Blend:  BlendMode(data[9]),
		Pixels: data[blitRectHeaderLen:],
	}, nil
}
```

オフセット定数（`blitRectHeaderLen`）を Encode と Decode が共有することで、
片側だけの変更が起きなくなる。

### 呼び出し側の変化

VRAM 側（`vram.go`）:

```go
// Before
func (v *VRAMModule) handleBlitRect(msg *bus.BusMessage) {
	if len(msg.Data) < 10 { return }
	page := int(msg.Data[0])
	dstX := int(binary.BigEndian.Uint16(msg.Data[1:]))
	// ... 手書きオフセット
}

// After
func (v *VRAMModule) handleBlitRect(msg *bus.BusMessage) {
	c, err := arcproto.DecodeBlitRect(msg.Data)
	if err != nil { return }   // 既存の「黙って return」を維持
	page := int(c.Page)
	// ... c.X, c.Y, c.W, c.H, c.Blend, c.Pixels を使用
}
```

CPU 側（`cpu.go`）— 本仕様では `publish*` の**内側だけ**を差し替え、シグネチャは変えない:

```go
func (c *CPUModule) publishBlitRect(b bus.Bus, page uint8, x, y, w, h uint16, blendMode uint8, pixels []uint8) {
	cmd := arcproto.BlitRect{
		Page: page, X: x, Y: y, W: w, H: h,
		Blend: arcproto.BlendMode(blendMode), Pixels: pixels,
	}
	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: cmd.Target(), Operation: bus.OpCommand,
		Data: cmd.Encode(), Source: c.Name(),
	})
}
```

シーン側のコードは無変更で済むため、差分が最小になり R6 の検証が容易になる。

### エラー設計

```go
var (
	ErrShortPayload = errors.New("arcproto: payload too short")
	ErrBadPayload   = errors.New("arcproto: malformed payload")
)
```

`vram.go` 側は現状の挙動（黙って `return`）を維持するが、
将来のログ出力・`page_error` 発行に備えて error を返す設計にしておく。

### 移行順序（各ステップでビルド可能を維持）

1. `arcproto` パッケージを新規追加（既存コードからは未使用）。単体テストを付ける。
2. `vram.go` の `handle*` を 1 つずつ Decode に置換。各置換後にテストを流す。
3. `cpu.go` の `publish*` を 1 つずつ Encode に置換。
4. `integration/*_test.go` のローカルヘルパーを置換。
5. トピック・Target の文字列リテラルを定数参照に置換。
6. `VRAM-Specification.md` を実装から書き起こす。

### 本仕様で扱わないこと（スコープ外）

- デモシーン（`scene1`〜`scene7`）の移動 → 027 / 029
- `batch` コマンドの追加 → 025
- `gob` エンベロープの変更 → 028
- `internal/bus` 側の変更（トピック前方一致・drop 挙動）→ 025
- 入力・vsync → 026
- SDK（`arc` パッケージ）→ 027

## 検証シナリオ (Verification Scenarios)

1. `features/neurom/arcproto/` を作成し、17 コマンド + 4 イベントの型・Encode・Decode を実装する
2. `arcproto` の単体テストを追加し、全コマンドのラウンドトリップ（R9）とゴールデンバイト列（R8）が PASS することを確認する
3. `arcproto` が `internal/bus` を import していないことを確認する
4. `vram.go` の `handle*` を `arcproto` の Decode に置換する
5. `cpu.go` の `publish*` を `arcproto` の Encode に置換する（`publish*` のシグネチャとシーン側コードは変更しない）
6. `integration/*_test.go` のローカルエンコードヘルパーを `arcproto` に置換する
7. トピック名・Target 名の文字列リテラルを `arcproto` の定数参照に置換する
8. `scripts/process/build.sh` を実行し、ビルドと全単体テスト（`integration/` 含む）が PASS することを確認する
9. `./bin/neurom.exe` を起動し、デモ 7 シーンが置換前と同じ見た目で動作することを目視確認する
10. `./bin/neurom.exe --headless --stats-port 8080` を起動し、`/stats` に現れるコマンド名が置換前と一致することを確認する
11. `prompts/specifications/VRAM-Specification.md` を実装ベースの内容で埋める
12. 新仕様書のコマンド一覧が `vram.go` の `handleMessage` の switch 分岐と 1:1 で対応していることを突き合わせ確認する

## テスト項目 (Testing for the Requirements)

| 要件 | 検証方法 | コマンド / 手段 |
|---|---|---|
| R1: パッケージ新設と可視性 | `arcproto` が `internal/` 外に存在し、`internal/bus` を import していない | `ls features/neurom/arcproto/` および `cd features/neurom && go list -deps ./arcproto \| grep internal/bus`（0 件であること） |
| R2: 定数化 | 残存する文字列リテラルが 0 件 | `grep -rn '"blit_rect"\|"clear_vram"\|"vram_update"' features/neurom/internal/ features/neurom/integration/`（`arcproto` 内の定義箇所以外 0 件） |
| R3: Encode / Decode 対称性 | 全コマンドのラウンドトリップテスト PASS | `cd features/neurom && go test -v -count=1 -run "TestRoundTrip" ./arcproto/...` |
| R4: 単位型 | `Degrees` / `Turns` / `ScaleOf` の境界値テスト PASS | `cd features/neurom && go test -v -count=1 -run "TestRotation\|TestScale" ./arcproto/...` |
| R5: 呼び出し元置換 | `internal/modules/` 内に手書きオフセット計算が残っていない | `grep -rn "binary.BigEndian.PutUint16" features/neurom/internal/modules/`（`arcproto` 委譲後は 0 件を目標） |
| R6: 振る舞い不変（自動） | ゴールデンバイト列テスト PASS + 既存全テスト PASS | `scripts/process/build.sh` |
| R6: 振る舞い不変（統合） | VRAM 系統合テスト全件 PASS | `scripts/process/integration_test.sh --specify "TestPageManagementIntegration\|TestPageDrawIsolationIntegration\|TestPaletteUpdate\|TestVRAMMonitorIntegration"` |
| R6: 振る舞い不変（目視） | デモ 7 シーンの表示が不変 | `./bin/neurom.exe` を 21 秒以上起動し全シーンを確認 |
| R6: 統計名の不変 | `/stats` のコマンド名が置換前と一致 | `./bin/neurom.exe --headless --stats-port 8080` + `cd features/stats && go run . --endpoint http://localhost:8080/stats` |
| R7: 仕様書作成 | コマンド一覧が実装の switch 分岐と 1:1 対応 | `VRAM-Specification.md` と `vram.go` `handleMessage` の目視突き合わせ |
| R8: ゴールデンバイト列 | 固定値バイト列テスト PASS | `cd features/neurom && go test -v -count=1 -run "TestGolden" ./arcproto/...` |
| R9: ラウンドトリップ | R3 と同一 | 同上 |
| 全体リグレッション | ビルド + 全単体テスト | `scripts/process/build.sh` |

### 補足: 検証スクリプトの現状

- `scripts/process/build.sh` は `features/*/` を走査し、`go list ./... | grep -v '/tests/'` で
  単体テストを実行する。`features/neurom/integration/` はパス名が `/tests/` に一致しないため
  **`build.sh` の対象に含まれる**。したがって `build.sh` が実質的な全体検証ゲートである。
- `scripts/process/integration_test.sh` はリポジトリルートの `tests/go.mod` の存在を前提とするが、
  現状 `tests/` ディレクトリは存在しないため warn を出して exit 0 する（実質 no-op）。
  上表の `integration_test.sh --specify` は、統合テストが `tests/` へ移設された後に有効となる。
  **それまでは `build.sh` と、必要に応じた `cd features/neurom && go test -run ... ./integration/...` で代替する。**

## 対応ステータス

- **ステータス**: 未着手（実装計画レビュー待ち）
- **実装計画**: [024-ARCProtocol-PackageExtraction.md](file://prompts/phases/000-foundation/branches/main/plans/024-ARCProtocol-PackageExtraction.md)
