# 006-VRAMEnhancement-Part2

> **Source Specification**: prompts/phases/000-foundation/ideas/main/006-VRAMEnhancement.md

## Goal Description
VRAMモジュールのエンハンスの後半部分を実装する。具体的には、アフィン変換（`blit_rect_transform`）コマンド、CPUモジュール内のデモプログラム、および全機能を検証する統合テストを実装する。

**前提**: Part 1（パレットRGBA化、二重バッファ、ブレンド、クリッピング、基本コマンド群）が完了していること。

## User Review Required
None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| REQ-3: アフィン変換 (`blit_rect_transform`) | Proposed Changes > `transform.go`, `vram.go` |
| REQ-11: デモプログラム | Proposed Changes > `cpu.go` |
| 統合テスト (全体パイプライン) | Proposed Changes > `vram_enhancement_test.go` |

## Proposed Changes

### transform (新規ファイル)

#### [NEW] [transform_test.go](file://features/neurom/internal/modules/vram/transform_test.go)
*   **Description**: アフィン変換計算の単体テスト。TDDで先に作成する。
*   **Technical Design**:
    *   ```go
        package vram

        func TestRotation90(t *testing.T)
        func TestRotationWithCustomPivot(t *testing.T)
        func TestScale2x(t *testing.T)
        func TestRotateAndScale(t *testing.T)
        ```
*   **Logic**:
    *   **TestRotation90**:
        *   4x4 のソースデータ（各行が異なるパレットインデックスで埋められた非対称パターン）を用意:
            ```
            src = [
                1, 1, 1, 1,  // row 0
                2, 2, 2, 2,  // row 1
                3, 3, 3, 3,  // row 2
                4, 4, 4, 4,  // row 3
            ]
            ```
        *   `TransformBlit(src, 4, 4, 2, 2, 64, 0x0100, 0x0100)` を呼び出し (中心pivot=(2,2), 90度回転, 等倍)
        *   出力の各ピクセル位置が90度回転後の正しい値であることを確認
        *   例: 出力の左上列は元の下辺のデータ (4,3,2,1) に対応するはず

    *   **TestRotationWithCustomPivot**:
        *   同じ4x4ソースデータで、pivot=(0,0) (左上) で90度回転
        *   出力結果が pivot=(2,2) の場合と異なることを確認
        *   例: pivot=(0,0) の場合、左上角を基準に回転するため、出力が大きくシフトする

    *   **TestScale2x**:
        *   2x2 のソースデータ `[1, 2, 3, 4]` を用意
        *   `TransformBlit(src, 2, 2, 1, 1, 0, 0x0200, 0x0200)` を呼び出し (回転なし, 2倍拡大)
        *   出力が 4x4 になり、各ピクセルが2x2に拡大されていること:
            ```
            expected = [
                1, 1, 2, 2,
                1, 1, 2, 2,
                3, 3, 4, 4,
                3, 3, 4, 4,
            ]
            ```

    *   **TestRotateAndScale**:
        *   4x4 のソースデータ、pivot=(2,2), 回転=64 (90度), scale=0x0200 (2倍)
        *   出力サイズが 8x8 であることを確認
        *   ソース範囲外のピクセルが `0` (書き込みスキップの indicator) であること

#### [NEW] [transform.go](file://features/neurom/internal/modules/vram/transform.go)
*   **Description**: アフィン変換（回転・拡大）のロジック。
*   **Technical Design**:
    *   ```go
        package vram

        import "math"

        // TransformBlit applies rotation and scaling to source pixel data
        // around the specified pivot point.
        // Returns the transformed pixel buffer and its dimensions.
        // Pixels outside the source range are set to 0 (transparent/skip marker).
        func TransformBlit(
            src []uint8, srcW, srcH int,
            pivotX, pivotY int,
            rotation uint8,
            scaleX, scaleY uint16,
        ) (dst []uint8, dstW, dstH int)
        ```
*   **Logic**:

    **角度の変換**:
    *   `angleRad := float64(rotation) / 256.0 * 2.0 * math.Pi`
    *   `cosA := math.Cos(angleRad)`, `sinA := math.Sin(angleRad)`

    **スケールの変換** (8.8固定小数点 → float64):
    *   `sx := float64(scaleX) / 256.0`
    *   `sy := float64(scaleY) / 256.0`

    **出力サイズの計算**:
    *   変換後の4隅の座標を計算して包含矩形を求める:
        ```
        corners = [(0,0), (srcW,0), (0,srcH), (srcW,srcH)]
        各cornerについて:
          dx = corner.x - pivotX
          dy = corner.y - pivotY
          tx = dx*cosA*sx - dy*sinA*sy
          ty = dx*sinA*sx + dy*cosA*sy
        ```
    *   `dstW = ceil(maxX - minX)`, `dstH = ceil(maxY - minY)`

    **出力オフセットの計算**:
    *   `offsetX = minX` (出力バッファ内での原点オフセット)
    *   `offsetY = minY`

    **逆変換によるピクセルマッピング**:
    *   出力バッファ `dst = make([]uint8, dstW*dstH)` を `0` で初期化
    *   出力の各ピクセル (outX, outY) についてループ:
        ```
        // 出力座標をpivot基準のローカル座標に変換
        fx := float64(outX) + offsetX
        fy := float64(outY) + offsetY

        // 逆アフィン変換 (逆回転 → 逆スケール)
        srcFX := (fx*cosA + fy*sinA) / sx + float64(pivotX)
        srcFY := (-fx*sinA + fy*cosA) / sy + float64(pivotY)

        // 最近傍補間
        srcPX := int(math.Round(srcFX))
        srcPY := int(math.Round(srcFY))

        // 範囲チェック
        if srcPX >= 0 && srcPX < srcW && srcPY >= 0 && srcPY < srcH {
            dst[outY*dstW+outX] = src[srcPY*srcW+srcPX]
        }
        // 範囲外は 0 のまま (スキップ)
        ```

---

### vram (追加コマンド)

#### [MODIFY] [vram.go](file://features/neurom/internal/modules/vram/vram.go)
*   **Description**: `blit_rect_transform` コマンドの処理を `handleMessage` に追加。
*   **Logic**:

    **`case "blit_rect_transform"`**:
    *   パース:
        ```
        dst_x  := int(msg.Data[0])<<8 | int(msg.Data[1])    // offset 0
        dst_y  := int(msg.Data[2])<<8 | int(msg.Data[3])    // offset 2
        srcW   := int(msg.Data[4])<<8 | int(msg.Data[5])    // offset 4
        srcH   := int(msg.Data[6])<<8 | int(msg.Data[7])    // offset 6
        pivotX := int(msg.Data[8])<<8 | int(msg.Data[9])    // offset 8
        pivotY := int(msg.Data[10])<<8 | int(msg.Data[11])  // offset 10
        rotation := msg.Data[12]                              // offset 12
        scaleX := uint16(msg.Data[13])<<8 | uint16(msg.Data[14]) // offset 13
        scaleY := uint16(msg.Data[15])<<8 | uint16(msg.Data[16]) // offset 15
        blendMode := BlendMode(msg.Data[17])                  // offset 17
        pixelData := msg.Data[18:]                            // offset 18
        ```
    *   長さ検証: `len(pixelData) >= srcW*srcH` でなければ return
    *   アフィン変換の適用:
        ```
        transformed, dstW, dstH := TransformBlit(pixelData, srcW, srcH, pivotX, pivotY, rotation, scaleX, scaleY)
        ```
    *   転送先座標の算出: `TransformBlit` が返す出力バッファの原点は pivot 基準のオフセットを含むため:
        ```
        // TransformBlit は内部で offsetX/offsetY を計算している
        // → TransformBlit の返却値に offsetX, offsetY も含める必要がある
        // 修正: func TransformBlit(...) (dst []uint8, dstW, dstH, offsetX, offsetY int)
        actualDstX := dst_x + offsetX
        actualDstY := dst_y + offsetY
        ```
    *   クリッピング: `cx, cy, cw, ch, srcOffX, srcOffY := ClipRect(actualDstX, actualDstY, dstW, dstH, v.width, v.height)`
    *   `cw <= 0` なら return
    *   描画ループ: `blit_rect` と同一のブレンド/直接書き込みロジック。ただし `transformed[i] == 0` のピクセルは書き込みスキップ(透明扱い)
    *   イベント: `"vram_update"` トピックに `"rect_updated"` ターゲットをパブリッシュ

#### [MODIFY] [vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)
*   **Description**: `blit_rect_transform` コマンドの単体テストを追加。
*   **Technical Design**:
    *   ```go
        func TestBlitRectTransform(t *testing.T)
        ```
*   **Logic**:
    *   4x4 の非対称パターンを用意
    *   `blit_rect_transform` メッセージを構築（pivot=中心, rotation=64, scale=等倍, blendMode=Replace）
    *   `v.handleMessage(msg)` 呼び出し後、VRAM上の対応する座標のピクセルが90度回転後の値であることを検証

---

### cpu (デモプログラム)

#### [MODIFY] [cpu.go](file://features/neurom/internal/modules/cpu/cpu.go)
*   **Description**: 既存のデモロジックを置き換え、全VRAM新機能のデモシーンを実装する。
*   **Technical Design**:
    *   ```go
        // demoScene represents a single demo scene with init and update functions.
        type demoScene struct {
            name   string
            init   func(b bus.Bus)           // called once when scene starts
            update func(b bus.Bus, frame int) // called every frame (30fps)
        }

        func (c *CPUModule) run(ctx context.Context, b bus.Bus, sysCh <-chan *bus.BusMessage)
        ```
*   **Logic**:

    **`run()` メソッドの書き換え**:
    ```
    1. b.Publish("vram", mode初期化)
    2. time.Sleep(300ms) // VRAM/Monitor初期化待ち
    3. scenes := []demoScene{scene1, scene2, scene3, scene4, scene5}
    4. sceneIdx := 0
    5. scenes[sceneIdx].init(b)
    6. sceneDuration := 3 * time.Second
    7. sceneStartTime := time.Now()
    8. frame := 0

    9. ticker := time.NewTicker(33ms) // ~30fps
    10. for {
        select {
        case <-ctx.Done(): return
        case msg := <-sysCh: if shutdown → return
        case <-ticker.C:
            scenes[sceneIdx].update(b, frame)
            frame++
            if time.Since(sceneStartTime) > sceneDuration {
                sceneIdx = (sceneIdx + 1) % len(scenes)
                scenes[sceneIdx].init(b)
                frame = 0
                sceneStartTime = time.Now()
            }
        }
    }
    ```

    **Scene 1: パレット一括設定 + VRAM初期化**:
    *   `init`:
        *   256色のHSVグラデーションパレットを生成（既存の `hsvToRGB` 関数を再利用）
        *   `set_palette_block` で一括設定（1回のメッセージで最大256色）
        *   `clear_vram` でインデックス `0` で初期化
    *   `update`:
        *   `clear_vram` のインデックスを `frame % 256` に設定し、背景色アニメーション

    **Scene 2: 矩形ブロック転送**:
    *   `init`:
        *   8x8 のチェッカーパターン生成 (偶数ピクセル=`idx1`, 奇数ピクセル=`idx2`)
        *   `clear_vram` で背景クリア
    *   `update`:
        *   画面全体に 8x8 タイルを敷き詰める `blit_rect` ループ
        *   `idx1`, `idx2` のパレット色をフレームごとに変化（`set_palette` でアニメーション）

    **Scene 3: アフィン変換**:
    *   `init`:
        *   16x16 の矢印型スプライトデータを定義 (配列リテラル)
        *   `clear_vram` で背景クリア
    *   `update`:
        *   `clear_vram` で毎フレーム背景クリア
        *   画面中央 (128, 106): `blit_rect_transform` で rotation=`frame*2 % 256`, pivot=(8,8) (中心), scale=等倍
        *   画面左 (64, 106): pivot=(8,16) (足元), 同じ rotation
        *   画面右 (192, 106): pivot=(8,8), scale=0x0200 (2倍), 同じ rotation

    **Scene 4: アルファブレンド**:
    *   `init`:
        *   パレット設定:
            *   idx=1: 赤 (255,0,0,255), idx=2: 緑 (0,255,0,255), idx=3: 青 (0,0,255,255)
            *   idx=10: 白半透明 (255,255,255,128)
        *   `clear_vram` で黒背景
        *   背景に3本の縦バー描画 (各約85px幅): `blit_rect` で赤、緑、青
    *   `update`:
        *   5つのブレンドモード (Replace, Alpha, Additive, Multiply, Screen) で
            横に並べた矩形（各約51px幅 × 高さ適当）を `blit_rect` で idx=10 を使って描画
        *   各モード名ラベルはピクセルアートでは困難なため省略、位置で区別

    **Scene 5: スクロール**:
    *   `init`:
        *   カラフルな横縞パターンを画面全体に `blit_rect` で描画 (各行のパレットインデックスが異なる)
    *   `update`:
        *   `copy_rect` で (0, 1, 0, 0, 256, 211) → Y方向に1px上スクロール
        *   最下行 (y=211) に新しいパターンを `blit_rect` (幅256, 高さ1) で描画
        *   パレットインデックスをフレームに応じて変化させ、流れるようなアニメーション

    **ヘルパー関数**:
    *   `publishBlitRect(b bus.Bus, x, y, w, h int, blendMode uint8, pixels []byte) error` — `blit_rect` メッセージの構築とPublish
    *   `publishBlitRectTransform(b bus.Bus, x, y, w, h, pivotX, pivotY int, rotation uint8, scaleX, scaleY uint16, blendMode uint8, pixels []byte) error`
    *   `publishCopyRect(b bus.Bus, srcX, srcY, dstX, dstY, w, h int) error`
    *   `publishClearVRAM(b bus.Bus, paletteIdx byte) error`
    *   `publishSetPaletteBlock(b bus.Bus, startIdx byte, colors [][4]byte) error`

---

### integration (統合テスト)

#### [NEW] [vram_enhancement_test.go](file://features/neurom/integration/vram_enhancement_test.go)
*   **Description**: 新規VRAM機能の統合テスト。VRAM + Monitor のバス連携パイプラインを検証。
*   **Technical Design**:
    *   ```go
        package integration

        func TestBlitAndReadRect(t *testing.T)
        func TestAlphaBlendPipeline(t *testing.T)
        func TestDemoProgram(t *testing.T)
        ```
*   **Logic**:

    **TestBlitAndReadRect**:
    1. ZMQ バスを `"inproc://test-blit-bus"` で作成
    2. VRAM + Monitor (headless) を起動
    3. `vram_update` チャンネルを外部からサブスクライブ
    4. `set_palette` でインデックス 5 を (100, 200, 50, 255) に設定
    5. 100ms 待機
    6. `blit_rect` で (10, 20, 4, 4) にインデックス 5 の4x4ブロックを書き込み (blendMode=Replace)
    7. 100ms 待機
    8. `read_rect` で (10, 20, 4, 4) を読み出しリクエスト
    9. バスから `"rect_data"` ターゲットのレスポンスを受信 (タイムアウト1秒)
    10. レスポンスのピクセルデータが全て `5` であることをアサート
    11. Monitor の `GetPixel(10, 20)` が (100, 200, 50, 255) であることをアサート
    12. クリーンアップ: cancel + StopAll

    **TestAlphaBlendPipeline**:
    1. ZMQ バスを `"inproc://test-blend-bus"` で作成
    2. VRAM + Monitor (headless) を起動
    3. パレット設定: idx=1 → 赤 (255, 0, 0, 255), idx=2 → 青 (0, 0, 255, 128)
    4. 100ms 待機
    5. 赤 (idx=1) の4x4矩形を (50, 50) に Replace モードで描画
    6. 100ms 待機
    7. 青 (idx=2) の4x4矩形を (50, 50) に AlphaBlend モードで描画
    8. 200ms 待機 (ブレンド結果のMonitor伝播待ち)
    9. Monitor の `GetPixel(50, 50)` が紫系 (128±5, 0, 127±5, 255) であることをアサート
    10. クリーンアップ

    **TestDemoProgram**:
    1. ZMQ バスを `"inproc://test-demo-bus"` で作成
    2. CPU + VRAM + Monitor (headless) を起動
    3. 10秒間のタイムアウト付きコンテキストで実行
    4. `vram_update` チャンネルから少なくとも1つのメッセージが到着することを確認
    5. 5秒後にキャンセルして正常終了
    6. パニックやデッドロックが発生しないことを確認

## Step-by-Step Implementation Guide

1.  [x] **transform_test.go の作成**:
    *   `features/neurom/internal/modules/vram/transform_test.go` を新規作成
    *   4つのアフィン変換テストケースを実装
    *   この時点でビルドは失敗する (TransformBlit 未定義)

2.  [x] **transform.go の作成**:
    *   `features/neurom/internal/modules/vram/transform.go` を新規作成
    *   `TransformBlit` 関数を実装 (逆変換アプローチ)
    *   `./scripts/process/build.sh` で変換テスト通過を確認

3.  [x] **vram.go に blit_rect_transform を追加**:
    *   `handleMessage` に `case "blit_rect_transform"` を追加
    *   `vram_test.go` に `TestBlitRectTransform` を追加
    *   `./scripts/process/build.sh` で確認

4.  [x] **cpu.go のデモプログラム実装**:
    *   既存の `run()` メソッドのデモロジックを置き換え
    *   `demoScene` 構造体とヘルパー関数を定義
    *   5つのシーンを実装
    *   `./scripts/process/build.sh` で確認

5.  [x] **統合テストの作成と実行**:
    *   `features/neurom/integration/vram_enhancement_test.go` を新規作成
    *   3つの統合テストケースを実装
    *   ```bash
        ./scripts/process/build.sh && ./scripts/process/integration_test.sh
        ```

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```
    *   transform_test.go, vram_test.go の全テストが PASS

2.  **Integration Tests**:
    ```bash
    ./scripts/process/integration_test.sh --specify "TestBlitAndReadRect"
    ./scripts/process/integration_test.sh --specify "TestAlphaBlendPipeline"
    ./scripts/process/integration_test.sh --specify "TestDemoProgram"
    ```
    *   全統合テストが PASS。デモテストはタイムアウト (10秒) 内に完了。

3.  **Regression Tests** (Part 1, 既存テストの回帰):
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   `TestVRAMMonitorIntegration`, `TestPaletteUpdate`, `TestShutdown` が引き続き PASS

## Documentation

#### [MODIFY] [006-VRAMEnhancement.md](file://prompts/phases/000-foundation/ideas/main/006-VRAMEnhancement.md)
*   **更新内容**: 全要件の実装が完了したことを記録
