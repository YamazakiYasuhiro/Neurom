# 006-VRAMEnhancement-Part1

> **Source Specification**: prompts/phases/000-foundation/ideas/main/006-VRAMEnhancement.md

## Goal Description
VRAMモジュールの大幅なエンハンスの前半部分を実装する。具体的には、パレットのRGBA拡張、二重バッファ構造の導入、ブレンドエンジン、クリッピング処理、および基本コマンド群（`blit_rect`, `read_rect`, `copy_rect`, `clear_vram`, `set_palette_block`, `read_palette_block`）を実装する。

**Part 2** では、アフィン変換（`blit_rect_transform`）、デモプログラム、統合テストを実装する。

## User Review Required

> [!IMPORTANT]
> **二重バッファ構造の導入**について: ブレンド描画をサポートするため、従来の `buffer []uint8`（パレットインデックス）に加えて `colorBuffer []uint8`（RGBA直接色）を追加します。これにより、メモリ使用量が `256*212` バイトから `256*212*5` バイトに増加します（約271KB → 約271KB + 217KB）。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| REQ-1: パレットのRGBA拡張 | Proposed Changes > `vram.go`, `monitor.go` |
| REQ-2: 矩形VRAMブロック書き込み (`blit_rect`) | Proposed Changes > `vram.go` |
| REQ-4: 矩形VRAMブロック読み出し (`read_rect`) | Proposed Changes > `vram.go` |
| REQ-5: 矩形VRAMコピー (`copy_rect`) | Proposed Changes > `vram.go` |
| REQ-6: VRAM初期化 (`clear_vram`) | Proposed Changes > `vram.go` |
| REQ-7: パレットブロック書き込み (`set_palette_block`) | Proposed Changes > `vram.go` |
| REQ-8: パレットブロック読み出し (`read_palette_block`) | Proposed Changes > `vram.go` |
| REQ-9: アルファブレンド描画 | Proposed Changes > `blend.go` |
| REQ-10: VRAM境界クリッピング | Proposed Changes > `vram.go` (ClipRect関数) |
| REQ-3: アフィン変換 (`blit_rect_transform`) | **Part 2 で実装** |
| REQ-11: デモプログラム | **Part 2 で実装** |

## Proposed Changes

### blend (新規ファイル)

#### [NEW] [blend_test.go](file://features/neurom/internal/modules/vram/blend_test.go)
*   **Description**: ブレンド計算の単体テスト。TDDで先に作成する。
*   **Technical Design**:
    *   ```go
        package vram

        func TestBlendReplace(t *testing.T)
        func TestBlendAlpha(t *testing.T)
        func TestBlendAdditive(t *testing.T)
        func TestBlendMultiply(t *testing.T)
        func TestBlendScreen(t *testing.T)
        ```
*   **Logic**:
    *   テーブル駆動テスト形式。各テストで `BlendPixel` を呼び出し、期待値と比較。
    *   **TestBlendReplace**:
        *   入力: `src=[100, 150, 200, 128]`, `dst=[50, 50, 50, 255]`
        *   期待: `[100, 150, 200, 128]` (srcがそのまま返る)
    *   **TestBlendAlpha**:
        *   入力: `src=[255, 0, 0, 128]` (赤, α≈0.5), `dst=[0, 0, 255, 255]` (青)
        *   計算: `R = 255*128/255 + 0*(255-128)/255 ≈ 128`, `G = 0`, `B = 0*128/255 + 255*(255-128)/255 ≈ 127`
        *   期待: `[128, 0, 127, 255]` (±1の丸め誤差許容)
    *   **TestBlendAdditive**:
        *   入力: `src=[200, 200, 200, 128]`, `dst=[100, 100, 100, 255]`
        *   計算: `R = min(200*128/255 + 100, 255) = min(200, 255) = 200`
        *   期待: 各チャンネルが加算され、255でクランプされること
    *   **TestBlendMultiply**:
        *   入力: `src=[128, 128, 128, 255]`, `dst=[200, 100, 50, 255]`
        *   計算: `R = 128*200/255 ≈ 100`, `G = 128*100/255 ≈ 50`, `B = 128*50/255 ≈ 25`
        *   期待: 各チャンネルが乗算されること
    *   **TestBlendScreen**:
        *   入力: `src=[128, 128, 128, 255]`, `dst=[100, 100, 100, 255]`
        *   計算: `R = 255 - (255-128)*(255-100)/255 = 255 - 127*155/255 ≈ 255-77 = 178`
        *   期待: スクリーン合成結果が正しいこと

#### [NEW] [blend.go](file://features/neurom/internal/modules/vram/blend.go)
*   **Description**: ブレンド計算ロジックを独立ファイルとして実装。
*   **Technical Design**:
    *   ```go
        package vram

        // BlendMode represents the pixel blending mode.
        type BlendMode uint8

        const (
            BlendReplace  BlendMode = 0x00
            BlendAlpha    BlendMode = 0x01
            BlendAdditive BlendMode = 0x02
            BlendMultiply BlendMode = 0x03
            BlendScreen   BlendMode = 0x04
        )

        // BlendPixel computes the blended RGBA output for a single pixel.
        // src/dst are [R, G, B, A] arrays.
        // For Alpha/Additive modes, alpha is taken from src[3].
        func BlendPixel(mode BlendMode, src, dst [4]uint8) [4]uint8
        ```
*   **Logic**:
    *   `BlendReplace`: `return src` — ソースをそのまま返す。
    *   `BlendAlpha`: 各RGBチャンネルについて整数演算で計算:
        ```
        alpha := uint16(src[3])
        invAlpha := 255 - alpha
        R = uint8((uint16(src[0])*alpha + uint16(dst[0])*invAlpha) / 255)
        G = uint8((uint16(src[1])*alpha + uint16(dst[1])*invAlpha) / 255)
        B = uint8((uint16(src[2])*alpha + uint16(dst[2])*invAlpha) / 255)
        A = 255
        ```
    *   `BlendAdditive`: 各RGBチャンネルについて:
        ```
        alpha := uint16(src[3])
        R = uint8(min(uint16(src[0])*alpha/255 + uint16(dst[0]), 255))
        // G, B も同様
        A = 255
        ```
    *   `BlendMultiply`: 各RGBチャンネルについて:
        ```
        R = uint8(uint16(src[0]) * uint16(dst[0]) / 255)
        // G, B も同様 (αは使用しない)
        A = 255
        ```
    *   `BlendScreen`: 各RGBチャンネルについて:
        ```
        R = uint8(255 - uint16(255-src[0]) * uint16(255-dst[0]) / 255)
        // G, B も同様 (αは使用しない)
        A = 255
        ```

---

### vram (既存ファイル修正 + テスト)

#### [MODIFY] [vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)
*   **Description**: 新規コマンドの単体テストを追加。TDDで先に作成する。
*   **Technical Design**:
    *   ```go
        func TestSetPaletteRGBA(t *testing.T)      // REQ-1
        func TestClearVRAM(t *testing.T)            // REQ-6
        func TestBlitRect(t *testing.T)             // REQ-2
        func TestReadRect(t *testing.T)             // REQ-4
        func TestCopyRect(t *testing.T)             // REQ-5
        func TestSetPaletteBlock(t *testing.T)      // REQ-7
        func TestReadPaletteBlock(t *testing.T)     // REQ-8
        func TestBlitRectWithBlend(t *testing.T)    // REQ-9
        func TestBlitRectClipping(t *testing.T)     // REQ-10
        func TestBlitRectFullyOutside(t *testing.T) // REQ-10
        func TestCopyRectClipping(t *testing.T)     // REQ-10
        ```
*   **Logic**:
    *   既存の `testBus` を再利用。各テストは `v := New()`, `v.bus = b` パターンで初期化し `v.handleMessage(msg)` を直接呼び出す。
    *   **TestSetPaletteRGBA**:
        *   ケース1 (後方互換4バイト): Data=`[0x01, 255, 0, 0]` → `v.palette[1]` が `[255, 0, 0, 255]` であること
        *   ケース2 (RGBA5バイト): Data=`[0x02, 0, 255, 0, 128]` → `v.palette[2]` が `[0, 255, 0, 128]` であること
    *   **TestClearVRAM**:
        *   VRAMに事前にピクセルを書き込んだ後、Target=`"clear_vram"`, Data=`[0x03]` を送信
        *   `v.buffer` の全要素が `0x03` であること
        *   `v.colorBuffer` の全ピクセルが `palette[3]` のRGBA値であること
    *   **TestBlitRect**:
        *   4x4, 全ピクセル=`0x05` のデータを座標(10,20)に書き込み
        *   `v.buffer[20*256+10]` 〜 `v.buffer[23*256+13]` が全て `0x05`
        *   周囲 (`v.buffer[20*256+9]`, `v.buffer[20*256+14]`) が `0x00`
    *   **TestReadRect**:
        *   事前に `blit_rect` で書き込み後、Target=`"read_rect"` を送信
        *   `testBus` のチャンネルに `"rect_data"` ターゲットのメッセージが来ること
        *   レスポンスのpixel_data部分を検証
    *   **TestCopyRect**:
        *   ケース1 (重なりなし): (0,0)→(50,50) の32x32コピー。コピー先の内容がソースと一致
        *   ケース2 (重なりあり): (0,0)→(0,8) の32x32コピー。内部一時バッファにより正しくコピーされていること
    *   **TestSetPaletteBlock**:
        *   開始インデックス=`0`, count=`16`, 16色分のRGBAデータ送信
        *   `v.palette[0]` 〜 `v.palette[15]` が設定通りであること
    *   **TestReadPaletteBlock**:
        *   事前にパレット設定後、Target=`"read_palette_block"`, Data=`[0x00, 0x10]` (start=0, count=16) を送信
        *   レスポンスの `"palette_data"` メッセージのデータ内容を検証
    *   **TestBlitRectWithBlend**:
        *   パレット1=赤(255,0,0,255), パレット2=青(0,0,255,128)を設定
        *   パレット1のベタ塗り矩形をReplace(0x00)で描画
        *   パレット2の矩形をAlphaBlend(0x01)で上書き描画
        *   重なり部分の `colorBuffer` が紫系 ([128, 0, 127, 255] ±1) であること
        *   重なり部分の `buffer` (indexBuffer) が `0xFF` (ダイレクトカラーマーカー) であること
    *   **TestBlitRectClipping**:
        *   座標(250, 200)に16x16矩形を書き込み
        *   x=250..255, y=200..211 の範囲のみ書き込まれていること
        *   x≥256, y≥212 のメモリ領域が変更されていないこと（バッファ溢れなし）
    *   **TestBlitRectFullyOutside**:
        *   座標(300, 300)に8x8矩形を書き込み → パニックやエラーなし、バッファ変更なし
    *   **TestCopyRectClipping**:
        *   パターンを(0,0)に書き込み後、(250, 200)にコピー → 範囲内のみコピーされること

#### [MODIFY] [vram.go](file://features/neurom/internal/modules/vram/vram.go)
*   **Description**: パレットRGBA化、二重バッファ構造、新コマンド群の追加、クリッピング処理。
*   **Technical Design**:
    *   ```go
        type VRAMModule struct {
            mu          sync.RWMutex
            width       int
            height      int
            color       int
            buffer      []uint8     // palette index buffer (renamed from 'buffer')
            colorBuffer []uint8     // direct color buffer (RGBA, len = width*height*4)
            palette     [256][4]uint8 // RGBA (changed from [3]uint8)
            bus         bus.Bus
            wg          sync.WaitGroup
        }

        func New() *VRAMModule
        // ClipRect clips a rectangle against VRAM bounds.
        func ClipRect(x, y, w, h, vramW, vramH int) (cx, cy, cw, ch, srcOffX, srcOffY int)
        ```
*   **Logic**:

    **`New()` の変更**:
    *   `colorBuffer` を `make([]uint8, 256*212*4)` で初期化する。
    *   `palette` フィールドの型を `[256][4]uint8` に変更。初期値は rgb=(0,0,0), a=255 とする。

    **`ClipRect` 関数** (新規追加):
    *   入力: `x, y, w, h` (書き込み先矩形), `vramW, vramH` (VRAM境界)
    *   処理:
        1. `srcOffX = 0`, `srcOffY = 0` (ソースデータ内のオフセット)
        2. `x < 0` の場合: `srcOffX = -x`, `w += x`, `x = 0`
        3. `y < 0` の場合: `srcOffY = -y`, `h += y`, `y = 0`
        4. `x + w > vramW` の場合: `w = vramW - x`
        5. `y + h > vramH` の場合: `h = vramH - y`
        6. `w <= 0 || h <= 0` の場合: 全て `0` を返す (完全範囲外)
    *   出力: `cx, cy, cw, ch, srcOffX, srcOffY`

    **`handleMessage` の拡張** — 新規 case の追加:

    *   **`case "set_palette"`** (既存の拡張):
        *   `len(msg.Data) == 4` の場合: 後方互換。`index, r, g, b := msg.Data[0..3]`, `v.palette[index] = [4]uint8{r, g, b, 255}`
        *   `len(msg.Data) >= 5` の場合: RGBA。`v.palette[index] = [4]uint8{r, g, b, msg.Data[4]}`
        *   イベント: `palette_updated` をパブリッシュ (data にはRGBAの4バイト含める)

    *   **`case "clear_vram"`**:
        *   `paletteIdx := uint8(0)` (デフォルト)
        *   `len(msg.Data) >= 1` の場合: `paletteIdx = msg.Data[0]`
        *   `for i := range v.buffer { v.buffer[i] = paletteIdx }`
        *   `colorBuffer` も `palette[paletteIdx]` のRGBA値で埋める:
            ```
            rgba := v.palette[paletteIdx]
            for i := 0; i < len(v.colorBuffer); i += 4 {
                v.colorBuffer[i] = rgba[0]
                v.colorBuffer[i+1] = rgba[1]
                v.colorBuffer[i+2] = rgba[2]
                v.colorBuffer[i+3] = rgba[3]
            }
            ```
        *   イベント: `vram_update` トピックに `"vram_cleared"` ターゲットをパブリッシュ

    *   **`case "blit_rect"`**:
        *   パース: `dst_x, dst_y, width, height` を uint16 ビッグエンディアンでデコード (オフセット 0,2,4,6)
        *   `blendMode := BlendMode(msg.Data[8])`
        *   `pixelData := msg.Data[9:]`
        *   長さ検証: `len(pixelData) >= width*height` でなければ return
        *   クリッピング: `cx, cy, cw, ch, srcOffX, srcOffY := ClipRect(dst_x, dst_y, width, height, v.width, v.height)`
        *   `cw <= 0` なら return (完全範囲外)
        *   描画ループ:
            ```
            for row := 0; row < ch; row++ {
                for col := 0; col < cw; col++ {
                    srcIdx := (srcOffY+row)*width + (srcOffX+col)
                    dstIdx := (cy+row)*v.width + (cx+col)
                    palIdx := pixelData[srcIdx]

                    if blendMode == BlendReplace {
                        v.buffer[dstIdx] = palIdx
                        rgba := v.palette[palIdx]
                        v.colorBuffer[dstIdx*4]   = rgba[0]
                        v.colorBuffer[dstIdx*4+1] = rgba[1]
                        v.colorBuffer[dstIdx*4+2] = rgba[2]
                        v.colorBuffer[dstIdx*4+3] = rgba[3]
                    } else {
                        srcRGBA := v.palette[palIdx]
                        dstRGBA := [4]uint8{
                            v.colorBuffer[dstIdx*4],
                            v.colorBuffer[dstIdx*4+1],
                            v.colorBuffer[dstIdx*4+2],
                            v.colorBuffer[dstIdx*4+3],
                        }
                        result := BlendPixel(blendMode, srcRGBA, dstRGBA)
                        v.buffer[dstIdx] = 0xFF // direct color marker
                        v.colorBuffer[dstIdx*4]   = result[0]
                        v.colorBuffer[dstIdx*4+1] = result[1]
                        v.colorBuffer[dstIdx*4+2] = result[2]
                        v.colorBuffer[dstIdx*4+3] = result[3]
                    }
                }
            }
            ```
        *   イベント: `"vram_update"` トピックに `"rect_updated"` ターゲットをパブリッシュ

    *   **`case "read_rect"`**:
        *   パース: `x, y, width, height` を uint16 ビッグエンディアンでデコード
        *   レスポンスバッファ: `resp := make([]byte, 8 + width*height)` (ヘッダ8バイト + pixel data)
        *   ヘッダにx, y, width, heightをエンコード
        *   ループ: 各ピクセルについて `v.buffer[...]` を読み出し。VRAM範囲外は `0` を返す
        *   イベント: `"vram_update"` トピックに Target=`"rect_data"`, Data=resp をパブリッシュ

    *   **`case "copy_rect"`**:
        *   パース: `src_x, src_y, dst_x, dst_y, width, height` を uint16 ビッグエンディアンでデコード
        *   一時バッファ: `tmpIdx := make([]uint8, width*height)`, `tmpColor := make([]uint8, width*height*4)`
        *   ソース読み出し: 範囲内のピクセルを一時バッファにコピー（範囲外は0）
        *   デスティネーションクリッピング: `ClipRect(dst_x, dst_y, width, height, v.width, v.height)`
        *   クリッピング後の書き込み: 一時バッファからデスティネーションへコピー（`buffer` と `colorBuffer` 両方）
        *   イベント: `"vram_update"` トピックに `"rect_copied"` ターゲットをパブリッシュ

    *   **`case "set_palette_block"`**:
        *   パース: `startIdx := msg.Data[0]`, `count := msg.Data[1]`
        *   長さ検証: `len(msg.Data) >= 2 + int(count)*4`
        *   ループ: `count` 回繰り返し、`v.palette[startIdx+i] = [4]uint8{R, G, B, A}`
        *   イベント: `"vram_update"` トピックに `"palette_block_updated"` をパブリッシュ

    *   **`case "read_palette_block"`**:
        *   パース: `startIdx := msg.Data[0]`, `count := msg.Data[1]`
        *   レスポンス: `resp := make([]byte, 2 + int(count)*4)`, ヘッダに startIdx, count
        *   ループ: `count` 回繰り返し、`v.palette[startIdx+i]` のRGBAをレスポンスに書き込み
        *   イベント: `"vram_update"` トピックに Target=`"palette_data"`, Data=resp をパブリッシュ

    **既存 `case "draw_pixel"` の更新**:
    *   `colorBuffer` も同時に更新するよう変更:
        ```
        v.buffer[y*v.width+x] = p
        rgba := v.palette[p]
        idx := (y*v.width + x) * 4
        v.colorBuffer[idx]   = rgba[0]
        v.colorBuffer[idx+1] = rgba[1]
        v.colorBuffer[idx+2] = rgba[2]
        v.colorBuffer[idx+3] = rgba[3]
        ```

    **既存 `case "mode"` の更新**:
    *   `colorBuffer` も再初期化するよう変更:
        ```
        v.buffer = make([]uint8, v.width*v.height)
        v.colorBuffer = make([]uint8, v.width*v.height*4)
        ```

---

### monitor

#### [MODIFY] [monitor.go](file://features/neurom/internal/modules/monitor/monitor.go)
*   **Description**: パレットRGBA対応と `buildFrame` での直接色バッファ参照。
*   **Technical Design**:
    *   ```go
        type MonitorModule struct {
            // ... 既存フィールド ...
            palette [256][4]uint8 // [3]uint8 → [4]uint8 に変更
        }
        ```
*   **Logic**:

    **`New` の変更**:
    *   パレットの初期化で4要素目（アルファ）を `255` に設定:
        ```
        m.palette[p][3] = 255
        ```

    **`receiveLoop` の変更**:
    *   `"palette_updated"` 受信時: `len(msg.Data) >= 5` の場合は `msg.Data[4]` をアルファとして設定。4バイトの場合はアルファ `255`。

    **`buildFrame` の変更**:
    *   `"vram_updated"` 受信時: pixel data が 5バイト以上の場合のみ処理。

    *   現在のMonitorは `vram_update` チャンネルのイベントからピクセル単位で vram/palette を内部に同期している。
    *   Part 1 では新しいイベントタイプ (`"rect_updated"`, `"vram_cleared"`, `"rect_copied"`, `"palette_block_updated"`) を受信した際に、Monitor 側の内部 vram/palette キャッシュを更新する処理を追加する。

    **新規イベントハンドリング**:
    *   `"vram_cleared"`: Monitor内部の `vram[]` を全て msg.Data[0] で埋め、`dirty = true`
    *   `"rect_updated"` / `"rect_copied"`: Monitor側の vram バッファの差分更新が複雑なため、Part 1 では `dirty = true` のみ設定し、次回 `buildFrame` 時にテクスチャ全体を再構築する方式とする。
    *   `"palette_block_updated"`: msg.Data をパースし、パレットキャッシュを一括更新。`dirty = true`

    **`buildFrame` の拡張** (直接色バッファ対応):
    *   現在は `vram[i]` → `palette[vram[i]]` で RGB を引いている。
    *   Part 1 の段階では、Monitor はVRAMモジュールの `colorBuffer` を直接参照できない（バスメッセージ経由の疎結合設計）ため、ブレンド結果の直接色バッファの同期は別途検討が必要。
    *   **暫定方式**: `"rect_updated"` イベントの Data にブレンド結果（RGBA ピクセル情報）を含めてMonitorに伝播する。具体的にはイベントの Data を拡張して `[isDirect:uint8][x:uint16][y:uint16][w:uint16][h:uint16][rgba_or_index_data:...]` の形式とする。
    *   ただし Part 1 では既存の `vram_updated` イベントとの互換性を維持するため、矩形全体のRGBAデータをイベントに載せる方式は高負荷であり、代わりに **Monitor内で独自の colorBuffer `directRGBA []byte` を保持** する方式を採用:
        *   `"rect_updated"` イベント受信時、Monitor側で VRAM から `vram_update` として送られてきたデータに基づいて自身の `directRGBA` バッファを更新。ただし現在の疎結合設計では、VRAM はブレンド結果のRGBAデータをイベントに含めて送るのが最もシンプル。
    *   **最終方式**: `blit_rect` でブレンドモードが使用された場合、VRAM モジュールは `"rect_updated"` イベントに以下のデータを含める:
        ```
        [blend_mode:uint8][x:uint16][y:uint16][w:uint16][h:uint16][pixel_or_rgba_data:...]
        ```
        *   `blend_mode == 0x00 (Replace)`: pixel_data はパレットインデックス配列。Monitor は通常通りパレット引き。
        *   `blend_mode != 0x00`: pixel_data は RGBA データ (width*height*4 バイト)。Monitor は `directRGBA` バッファに直接書き込み。
    *   Monitor の `directRGBA` バッファに値がある座標は、`buildFrame` で直接 RGBA をテクスチャに書き込む。

    **MonitorModule 構造体への追加**:
    *   ```go
        directRGBA []byte  // len = width*height*4, zero-initialized
        hasDirectPixel []bool // len = width*height, tracks which pixels use directRGBA
        ```

## Step-by-Step Implementation Guide

1.  [x] **blend_test.go の作成**:
    *   `features/neurom/internal/modules/vram/blend_test.go` を新規作成
    *   5つのブレンドモードのテーブル駆動テストを実装
    *   この時点でビルドは失敗する (BlendPixel 未定義)

2.  [x] **blend.go の作成**:
    *   `features/neurom/internal/modules/vram/blend.go` を新規作成
    *   `BlendMode` 型、定数、`BlendPixel` 関数を実装
    *   `./scripts/process/build.sh` でブレンドテスト通過を確認

3.  [x] **vram_test.go の拡張**:
    *   既存の `vram_test.go` に新規テスト関数を追加
    *   `TestSetPaletteRGBA`, `TestClearVRAM`, `TestBlitRect`, `TestReadRect`, `TestCopyRect`, `TestSetPaletteBlock`, `TestReadPaletteBlock`, `TestBlitRectWithBlend`, `TestBlitRectClipping`, `TestBlitRectFullyOutside`, `TestCopyRectClipping`
    *   この時点でビルドは失敗する (新規コマンド未実装)

4.  [x] **vram.go の拡張 — 構造体変更**:
    *   `VRAMModule` 構造体: `palette` を `[256][4]uint8` に変更、`colorBuffer` フィールド追加
    *   `New()`: `colorBuffer` の初期化追加
    *   `ClipRect` 関数を追加
    *   `case "mode"`: `colorBuffer` の再初期化を追加

5.  [x] **vram.go の拡張 — 既存コマンド修正**:
    *   `case "set_palette"`: 4バイト/5バイト対応
    *   `case "draw_pixel"`: `colorBuffer` 同時更新を追加

6.  [x] **vram.go の拡張 — 新規コマンド実装**:
    *   `case "clear_vram"` 実装
    *   `case "blit_rect"` 実装 (ブレンド対応、クリッピング対応)
    *   `case "read_rect"` 実装
    *   `case "copy_rect"` 実装
    *   `case "set_palette_block"` 実装
    *   `case "read_palette_block"` 実装
    *   `./scripts/process/build.sh` で全テスト通過を確認

7.  [x] **monitor.go の修正**:
    *   パレットを `[256][4]uint8` に変更
    *   `directRGBA`, `hasDirectPixel` フィールド追加
    *   新規イベントハンドリング追加 (`rect_updated`, `vram_cleared`, `rect_copied`, `palette_block_updated`)
    *   `buildFrame` で `hasDirectPixel` チェック追加
    *   `./scripts/process/build.sh` で全テスト通過を確認

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```
    *   全単体テスト (blend_test.go, vram_test.go) が PASS すること

2.  **Integration Tests** (既存テストの回帰確認):
    ```bash
    ./scripts/process/integration_test.sh --specify "TestPaletteUpdate"
    ./scripts/process/integration_test.sh --specify "TestVRAMMonitorIntegration"
    ```
    *   既存の統合テストがパレットRGBA拡張後も PASS すること

## Documentation

#### [MODIFY] [006-VRAMEnhancement.md](file://prompts/phases/000-foundation/ideas/main/006-VRAMEnhancement.md)
*   **更新内容**: 実装が完了した要件にチェックマークを追加（Part 1 完了後）

## 継続計画について

本計画は Part 1 です。以下の要件は Part 2 で実装します:

*   REQ-3: アフィン変換 (`blit_rect_transform`) — `transform.go`, `transform_test.go`
*   REQ-11: デモプログラム — `cpu.go` の `run()` メソッド置き換え
*   統合テスト: `TestBlitAndReadRect`, `TestAlphaBlendPipeline`, `TestDemoProgram`
