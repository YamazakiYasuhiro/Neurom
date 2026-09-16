# 016-VRAMFeatureReintegration-Part1

> **Source Specification**: `prompts/phases/000-foundation/ideas/main/016-VRAMFeatureReintegration.md`

## Goal Description

VRAMモジュールの基本拡張を再統合する。二重バッファ構造（パレットインデックス + 直接色RGBA）、パレットRGBA化、および7種の拡張コマンド（`clear_vram`, `blit_rect`, `blit_rect_transform`, `read_rect`, `copy_rect`, `set_palette_block`, `read_palette_block`）を `vram.go` に実装する。既存の `blend.go` / `transform.go` を統合して使用する。

## User Review Required

- `blit_rect` / `blit_rect_transform` のワイヤーフォーマットは、Part 2（ページ対応）でページ引数が先頭に追加される。Part 1 ではページなし（006仕様の原型フォーマット）で実装し、Part 2 で拡張する。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
|:---|:---|
| R1: VRAM二重バッファ構造 | Proposed Changes > vram.go — VRAMModule 構造体変更 |
| R2: clear_vram | Proposed Changes > vram.go — handleClearVRAM |
| R2: blit_rect | Proposed Changes > vram.go — handleBlitRect |
| R2: blit_rect_transform | Proposed Changes > vram.go — handleBlitRectTransform |
| R2: read_rect | Proposed Changes > vram.go — handleReadRect |
| R2: copy_rect | Proposed Changes > vram.go — handleCopyRect |
| R2: set_palette_block | Proposed Changes > vram.go — handleSetPaletteBlock |
| R2: read_palette_block | Proposed Changes > vram.go — handleReadPaletteBlock |
| R2: VRAM境界クリッピング | Proposed Changes > vram.go — clipRect |
| R2: blend.go 統合 | 既存ファイルをそのまま使用（変更なし） |
| R2: transform.go 統合 | 既存ファイルをそのまま使用（変更なし） |

## Proposed Changes

### vram モジュール

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

* **Description**: 拡張コマンドの単体テストを追加する（TDD: テストを先に記述）
* **Technical Design**:

  ```go
  // テストヘルパー: testBus は既存のまま利用

  func TestClearVRAM(t *testing.T)
  // - indexBuffer 全体が指定パレットインデックスで埋まること
  // - colorBuffer 全体が palette[idx] の RGBA で埋まること
  // - "vram_cleared" イベントが発行されること
  // - データ省略時（空）はインデックス 0 で初期化

  func TestBlitRect(t *testing.T)
  // - 4x4 矩形をインデックス5で (10,20) に書き込み
  // - indexBuffer の対応位置が 5 であること
  // - colorBuffer の対応位置が palette[5] の RGBA であること（Replace モード）
  // - 周囲が未変更であること
  // - "rect_updated" イベントが発行されること

  func TestBlitRectAlphaBlend(t *testing.T)
  // - パレット idx=1 を (255,0,0,255)、idx=2 を (0,0,255,128) に設定
  // - Replace で idx=1 の矩形を描画
  // - Alpha モードで idx=2 の矩形を上書き
  // - colorBuffer が紫系 (R≈127, G=0, B≈128, A=255) であること
  // - indexBuffer が 0xFF (ダイレクトカラーマーカー) であること

  func TestBlitRectTransform(t *testing.T)
  // - 4x4 スプライトを 90度回転 (rotation=64)、等倍で描画
  // - TransformBlit の結果が正しく indexBuffer/colorBuffer に反映されること
  // - クリッピングが正しく適用されること

  func TestReadRect(t *testing.T)
  // - clear_vram で 5 に初期化後、blit_rect で一部を 3 に上書き
  // - read_rect で読み出し、期待データと一致すること
  // - "rect_data" レスポンスイベントのデータ検証

  func TestCopyRect(t *testing.T)
  // - blit_rect で (0,0) に 8x8 のパターンを書き込み
  // - copy_rect で (0,0) → (20,20) にコピー
  // - (20,20) の indexBuffer がコピー元と一致すること

  func TestCopyRectOverlap(t *testing.T)
  // - 重なりありコピー: (0,0,8,8) → (0,2,8,8)
  // - 一時バッファ経由で正しくコピーされること

  func TestSetPaletteRGBA(t *testing.T)
  // - 4バイト (RGB): set_palette でアルファが 255 になること
  // - 5バイト (RGBA): set_palette でアルファが指定値になること

  func TestSetPaletteBlock(t *testing.T)
  // - 16色分の RGBA データを一括設定
  // - palette[start..start+15] が設定値と一致すること

  func TestReadPaletteBlock(t *testing.T)
  // - set_palette_block で設定後、read_palette_block で読み出し
  // - "palette_data" レスポンスイベントのデータが一致すること

  func TestBlitRectClipping(t *testing.T)
  // - (250, 200) に 16x16 の矩形 → 有効範囲 (250-255, 200-211) のみ書き込まれること
  // - 完全に範囲外 (300, 300) → 何も書き込まれず正常終了

  func TestCopyRectClipping(t *testing.T)
  // - コピー先が部分的に範囲外 → 範囲内のみコピー
  ```

* **Logic**: 各テストは `newTestVRAM()` でモジュールを生成し、`handleMessage` を直接呼び出して検証する。`testBus` のチャネルからイベントメッセージを受信して内容を確認する。

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

* **Description**: VRAMModule の構造体を拡張し、7種の拡張コマンドハンドラを追加する
* **Technical Design**:

  **構造体の変更:**
  ```go
  const DirectColorMarker = 0xFF

  type VRAMModule struct {
      mu          sync.RWMutex
      width       int
      height      int
      color       int
      indexBuffer []uint8          // was: buffer — palette index buffer
      colorBuffer []uint8          // NEW — RGBA direct color buffer (width*height*4)
      palette     [256][4]uint8    // was: [256][3]uint8 — RGBA に拡張
      bus         bus.Bus
      wg          sync.WaitGroup
      stats       *stats.Collector
  }

  func New() *VRAMModule {
      w, h := 256, 212
      return &VRAMModule{
          width:       w,
          height:      h,
          color:       8,
          indexBuffer: make([]uint8, w*h),
          colorBuffer: make([]uint8, w*h*4),
          stats:       stats.NewCollector(),
      }
  }
  ```

  **クリッピング関数:**
  ```go
  // clipRect clips rectangle (x,y,w,h) against bounds (bw,bh).
  // Returns clipped rect and source offset. If fully outside, cw=0 or ch=0.
  func clipRect(x, y, w, h, bw, bh int) (cx, cy, cw, ch, srcOffX, srcOffY int)
  ```

  **イベント発行ヘルパー:**
  ```go
  // publishRectEvent publishes a rect_updated event with pixel data.
  // mode: 0x00=compact (index only), 0x01=full (index+RGBA), 0x02=header-only
  func (v *VRAMModule) publishRectEvent(x, y, w, h int)
  ```

  **handleMessage switch への追加:**
  ```go
  case "clear_vram":    v.handleClearVRAM(msg)
  case "blit_rect":     v.handleBlitRect(msg)
  case "blit_rect_transform": v.handleBlitRectTransform(msg)
  case "read_rect":     v.handleReadRect(msg)
  case "copy_rect":     v.handleCopyRect(msg)
  case "set_palette_block":   v.handleSetPaletteBlock(msg)
  case "read_palette_block":  v.handleReadPaletteBlock(msg)
  ```

* **Logic**:

  **`handleClearVRAM`**: ワイヤーフォーマット `[palette_idx:u8]`（省略時は0）
  - `indexBuffer` 全体を `palette_idx` で埋める
  - `colorBuffer` 全体を `palette[palette_idx]` の RGBA で埋める
  - `vram_cleared` イベント発行（データ: `[palette_idx:u8]`）

  **`handleBlitRect`**: ワイヤーフォーマット `[dst_x:u16][dst_y:u16][w:u16][h:u16][blend_mode:u8][pixel_data...]`
  - `clipRect` でクリッピング計算
  - `BlendMode(blend_mode)` で分岐:
    - `BlendReplace`: `indexBuffer[dst] = src_idx`、`colorBuffer[dst*4..] = palette[src_idx]`
    - その他: `srcRGBA = palette[src_idx]`、`dstRGBA = colorBuffer[dst*4..]` を読み取り、`BlendPixel(mode, srcRGBA, dstRGBA)` で計算、結果を `colorBuffer` に書き込み、`indexBuffer[dst] = DirectColorMarker`
  - `publishRectEvent` で `rect_updated` を発行

  **`handleBlitRectTransform`**: ワイヤーフォーマット `[dst_x:u16][dst_y:u16][src_w:u16][src_h:u16][pivot_x:u16][pivot_y:u16][rotation:u8][scale_x:u16][scale_y:u16][blend_mode:u8][pixel_data...]`
  - `TransformBlit(pixel_data, srcW, srcH, pivotX, pivotY, rotation, scaleX, scaleY)` を呼び出してバッファ取得
  - 変換結果の各ピクセルについて、`dst_x + offsetX + outX`, `dst_y + offsetY + outY` の位置に書き込み
  - 値が 0（透明/スキップ）のピクセルはスキップ
  - ブレンドモードは `handleBlitRect` と同様に適用
  - クリッピング適用

  **`handleReadRect`**: ワイヤーフォーマット `[x:u16][y:u16][w:u16][h:u16]`
  - 指定範囲の `indexBuffer` データを読み出し
  - 範囲外は 0 で埋める
  - `rect_data` イベント発行: `[x:u16][y:u16][w:u16][h:u16][pixel_data...]`

  **`handleCopyRect`**: ワイヤーフォーマット `[src_x:u16][src_y:u16][dst_x:u16][dst_y:u16][w:u16][h:u16]`
  - ソース領域を一時バッファにコピー（重なり対策）
  - 一時バッファからデスティネーションに書き込み（`indexBuffer` と `colorBuffer` 両方）
  - クリッピング適用
  - `rect_copied` イベント発行

  **`handleSetPaletteBlock`**: ワイヤーフォーマット `[start:u8][count:u8][R:u8][G:u8][B:u8][A:u8]...`
  - `count` 回ループで `palette[start+i] = {R, G, B, A}`
  - `palette_block_updated` イベント発行

  **`handleReadPaletteBlock`**: ワイヤーフォーマット `[start:u8][count:u8]`
  - `palette[start..start+count-1]` を RGBA 形式でレスポンス
  - `palette_data` イベント発行: `[start:u8][count:u8][RGBA...]`

  **既存コマンドの修正:**
  - `draw_pixel`: `buffer` → `indexBuffer`、`colorBuffer` にも `palette[p]` を書き込む
  - `set_palette`: `[3]uint8` → `[4]uint8` に変更。4バイト時は A=255、5バイト時は A=msg.Data[4]
  - `mode`: `buffer` → `indexBuffer` + `colorBuffer` の再初期化

  **VRAMAccessor 用の公開メソッド（Part 2 で Monitor から使用）:**
  ```go
  func (v *VRAMModule) VRAMBuffer() []uint8      // indexBuffer を返す
  func (v *VRAMModule) VRAMColorBuffer() []uint8  // colorBuffer を返す
  func (v *VRAMModule) VRAMWidth() int
  func (v *VRAMModule) VRAMHeight() int
  func (v *VRAMModule) VRAMPalette() [256][4]uint8
  ```

### 統合テスト

#### [MODIFY] [features/neurom/integration/vram_enhancement_test.go](file://features/neurom/integration/vram_enhancement_test.go)

* **Description**: Bus 経由の blit_rect + read_rect パイプライン検証、ブレンド検証を追加
* **Technical Design**:
  ```go
  func TestBlitAndReadRect(t *testing.T)
  // 1. ChannelBus + Manager で VRAM + Monitor を起動
  // 2. set_palette でパレット設定
  // 3. blit_rect で 4x4 矩形を書き込み
  // 4. read_rect で読み出し、"rect_data" レスポンスを検証
  // 5. Monitor.GetPixel で表示結果を確認

  func TestAlphaBlendPipeline(t *testing.T)
  // 1. パレット idx=1=(255,0,0,255)、idx=2=(0,0,255,128) を設定
  // 2. blit_rect Replace で idx=1 の矩形を描画
  // 3. blit_rect Alpha で idx=2 の矩形を上書き
  // 4. Monitor.GetPixel で R≈127, G=0, B≈128 を確認
  ```

## Step-by-Step Implementation Guide

- [x] 1. **vram_test.go にテスト追加**: `TestClearVRAM`, `TestSetPaletteRGBA` を追加
- [x] 2. **VRAMModule 構造体の変更**: `buffer` → `indexBuffer`、`colorBuffer` 追加、`palette` を `[256][4]uint8` に拡張、`New()` 更新
- [x] 3. **既存コマンドの修正**: `draw_pixel`、`set_palette`、`mode` を新構造体に合わせて更新
- [x] 4. **ビルド確認**: `./scripts/process/build.sh` — 既存テストが通ること
- [x] 5. **`clipRect` 関数の実装**
- [x] 6. **`handleClearVRAM` の実装**: テスト `TestClearVRAM` がパスすること
- [x] 7. **vram_test.go に `TestBlitRect`, `TestBlitRectClipping` を追加**
- [x] 8. **`handleBlitRect` の実装**: Replace モード → テストパス確認
- [x] 9. **vram_test.go に `TestBlitRectAlphaBlend` を追加**
- [x] 10. **`handleBlitRect` ブレンドモード拡張**: Alpha/Additive/Multiply/Screen → テストパス確認
- [x] 11. **vram_test.go に `TestBlitRectTransform` を追加**
- [x] 12. **`handleBlitRectTransform` の実装**: `TransformBlit` 呼び出し統合 → テストパス確認
- [x] 13. **vram_test.go に `TestReadRect` を追加**
- [x] 14. **`handleReadRect` の実装** → テストパス確認
- [x] 15. **vram_test.go に `TestCopyRect`, `TestCopyRectOverlap`, `TestCopyRectClipping` を追加**
- [x] 16. **`handleCopyRect` の実装**（一時バッファ方式）→ テストパス確認
- [x] 17. **vram_test.go に `TestSetPaletteBlock`, `TestReadPaletteBlock` を追加**
- [x] 18. **`handleSetPaletteBlock`, `handleReadPaletteBlock` の実装** → テストパス確認
- [x] 19. **VRAMAccessor 用メソッド追加**: `VRAMBuffer()`, `VRAMColorBuffer()`, `VRAMWidth()`, `VRAMHeight()`, `VRAMPalette()`
- [x] 20. **Monitor の palette を `[256][4]uint8` に変更**: `buildFrame` で `palette[p][3]`（A）も参照
- [x] 21. **統合テスト追加**: `TestBlitAndReadRect`, `TestAlphaBlendPipeline` を `vram_enhancement_test.go` に追加
- [x] 22. **ビルド確認**: `./scripts/process/build.sh`
- [x] 23. **統合テスト確認**: ビルドパイプラインで統合テスト含め全テスト通過確認済

## Verification Plan

### Automated Verification

1. **Build & Unit Tests**:
   ```bash
   ./scripts/process/build.sh
   ```
   - vram パッケージの全テスト（既存 + 新規）がパスすること
   - blend_test.go、transform_test.go の既存テストがパスすること

2. **Integration Tests**:
   ```bash
   ./scripts/process/integration_test.sh --specify TestBlitAndReadRect
   ./scripts/process/integration_test.sh --specify TestAlphaBlendPipeline
   ./scripts/process/integration_test.sh --specify TestDemoProgram
   ```
   - `TestDemoProgram`: 既存のパレットデモが新構造体でも動作すること
   - 既存の `TestHTTPStatsIntegration` が通ること（stats 機能との共存）

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/plans/main/016-VRAMFeatureReintegration-Part1.md](file://prompts/phases/000-foundation/plans/main/016-VRAMFeatureReintegration-Part1.md)
* **更新内容**: 各ステップの `[ ]` → `[x]` で進捗管理

## 継続計画について

Part 2 で以下を実装する:
- `pageBuffer` 構造体導入、ページ管理コマンド、全コマンドのページ引数化
- ページ単位サイズ管理、ビューポートオフセット
- Monitor の `VRAMAccessor` インターフェース、拡張イベントハンドラ

Part 3 で以下を実装する:
- CPU デモ 7 シーン、`main.go` の ChannelBus + TCPBridge 統合
