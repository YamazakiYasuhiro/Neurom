# 011-TransformFloatPrecision

> **Source Specification**: prompts/phases/000-foundation/ideas/main/011-RotationFloatPrecision.md

## Goal Description

`TransformBlit` 関数の回転角度(`rotation`)と拡大率(`scaleX`, `scaleY`)のパラメータを、バイトコード的な固定精度型（`uint8`, `uint16` 8.8固定小数点）から `float32` に変更する。あわせてバスメッセージのワイヤフォーマットとテストコードを更新する。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 要件1: `rotation` を `uint8` → `float32`（ラジアン直接指定） | Proposed Changes > transform.go, transform_test.go |
| 要件2: `scaleX/scaleY` を `uint16` → `float32`（1.0 = 等倍） | Proposed Changes > transform.go, transform_test.go |
| 要件3: ワイヤフォーマット更新（ヘッダ 18 → 25バイト、f32×3） | Proposed Changes > vram.go, vram_test.go |
| 要件4: 既存テストの更新 | Proposed Changes > transform_test.go, vram_test.go |
| 要件5: 後方互換性は不要 | 全体方針（旧フォーマット対応コード不要） |

## Proposed Changes

### vram module

#### [MODIFY] [transform_test.go](file://features/neurom/internal/modules/vram/transform_test.go)

*   **Description**: 全テストケースの `rotation` と `scaleX/scaleY` 引数を新しい型に変更。新規テストケースを追加。
*   **Technical Design**:
    *   `TransformBlit` 呼び出しの引数型を変更:
        *   `rotation`: `uint8` → `float32`（ラジアン）
        *   `scaleX`, `scaleY`: `uint16` → `float32`（倍率直値）
    *   値の変換対応:
        *   回転: `64` → `float32(math.Pi / 2)`, `0` → `0`
        *   スケール: `0x0100` → `float32(1.0)`, `0x0200` → `float32(2.0)`
*   **Logic**:
    *   `TestRotation90`: `TransformBlit(src, 4, 4, 2, 2, float32(math.Pi/2), 1.0, 1.0)` に変更
    *   `TestRotationWithCustomPivot`: 2箇所の呼び出しを同様に変更
    *   `TestScale2x`: `TransformBlit(src, 2, 2, 1, 1, 0, 2.0, 2.0)` に変更
    *   `TestRotateAndScale`: `TransformBlit(src, 4, 4, 2, 2, float32(math.Pi/2), 2.0, 2.0)` に変更
    *   新規: `TestArbitraryAngle` — `float32(math.Pi / 6)`（30°）で回転し、出力バッファが生成され非ゼロピクセルが存在することを確認
    *   新規: `TestArbitraryScale` — `1.5` と `0.75` で各軸スケーリングし、出力サイズが期待通りであることを確認

#### [MODIFY] [transform.go](file://features/neurom/internal/modules/vram/transform.go)

*   **Description**: 関数シグネチャと内部変換ロジックを変更。
*   **Technical Design**:
    ```go
    func TransformBlit(
        src []uint8, srcW, srcH int,
        pivotX, pivotY int,
        rotation float32,
        scaleX, scaleY float32,
    ) (dst []uint8, dstW, dstH, offsetX, offsetY int)
    ```
*   **Logic**:
    *   回転の変換ロジックを変更:
        *   変更前: `angleRad := float64(rotation) / 256.0 * 2.0 * math.Pi`
        *   変更後: `angleRad := float64(rotation)`
    *   スケールの変換ロジックを変更:
        *   変更前: `sx := float64(scaleX) / 256.0` / `sy := float64(scaleY) / 256.0`
        *   変更後: `sx := float64(scaleX)` / `sy := float64(scaleY)`
    *   `<= 0` ガードは維持（`sx <= 0` → `sx = 1.0`、`sy <= 0` → `sy = 1.0`）
    *   それ以降のバウンディングボックス計算、逆変換サンプリングのロジックは変更なし

#### [MODIFY] [vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

*   **Description**: `buildBlitRectTransformMsg` ヘルパーと関連テストを新ワイヤフォーマットに対応。
*   **Technical Design**:
    *   `buildBlitRectTransformMsg` のシグネチャ変更:
        ```go
        func buildBlitRectTransformMsg(
            dstX, dstY, srcW, srcH, pivotX, pivotY int,
            rotation float32, scaleX, scaleY float32, blendMode uint8,
            pixels []byte,
        ) *bus.BusMessage
        ```
    *   ヘッダサイズ: `18` → `25`
*   **Logic**:
    *   `buildBlitRectTransformMsg` 内部のシリアライズ:
        *   offset 0-11: 既存の `u16` フィールド6つ（`dstX`, `dstY`, `srcW`, `srcH`, `pivotX`, `pivotY`）は変更なし
        *   offset 12-15: `rotation` を `math.Float32bits(rotation)` → `binary.BigEndian.PutUint32(data[12:16], ...)` で書き込み
        *   offset 16-19: `scaleX` を `math.Float32bits(scaleX)` → `binary.BigEndian.PutUint32(data[16:20], ...)` で書き込み
        *   offset 20-23: `scaleY` を `math.Float32bits(scaleY)` → `binary.BigEndian.PutUint32(data[20:24], ...)` で書き込み
        *   offset 24: `blendMode`
        *   offset 25〜: `pixels`
    *   データバッファ確保: `make([]byte, 25+len(pixels))`
    *   `import` に `"encoding/binary"` と `"math"` を追加
    *   `buildBlitRectTransformMsg` 呼び出し箇所の引数変更（5箇所）:
        *   `TestBlitRectTransform`: `64, 0x0100, 0x0100` → `float32(math.Pi/2), float32(1.0), float32(1.0)`
        *   `TestTransformExact_NoRotation`: `0, 0x0100, 0x0100` → `0, float32(1.0), float32(1.0)`
        *   `TestTransformExact_Rotate90`（2箇所）: `64, 0x0100, 0x0100` → `float32(math.Pi/2), float32(1.0), float32(1.0)`
        *   `TestTransformExact_Scale2x`: `0, 0x0200, 0x0200` → `0, float32(2.0), float32(2.0)`
    *   `TransformBlit` 直接呼び出し箇所の引数変更（2箇所）:
        *   `TestTransformExact_Rotate90`: `TransformBlit(src, 4, 4, 2, 2, 64, 0x0100, 0x0100)` → `TransformBlit(src, 4, 4, 2, 2, float32(math.Pi/2), 1.0, 1.0)`
        *   `TestTransformExact_Scale2x`: `TransformBlit(src, 2, 2, 1, 1, 0, 0x0200, 0x0200)` → `TransformBlit(src, 2, 2, 1, 1, 0, 2.0, 2.0)`

#### [MODIFY] [vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: `handleBlitRectTransform` のデシリアライズロジックを新ワイヤフォーマットに対応。
*   **Technical Design**:
    *   `import` に `"encoding/binary"` と `"math"` を追加
    *   コメントのフォーマット記述を更新
*   **Logic**:
    *   最小ヘッダ長チェック: `len(data) < 18` → `len(data) < 25`
    *   offset 0-11: 既存の6つの `u16` フィールド（変更なし）
    *   offset 12-15: `rotation` のデシリアライズ
        *   変更前: `rotation := data[12]`
        *   変更後: `rotation := math.Float32frombits(binary.BigEndian.Uint32(data[12:16]))`
    *   offset 16-19: `scaleX` のデシリアライズ
        *   変更前: `scaleXVal := uint16(data[13])<<8 | uint16(data[14])`
        *   変更後: `scaleXVal := math.Float32frombits(binary.BigEndian.Uint32(data[16:20]))`
    *   offset 20-23: `scaleY` のデシリアライズ
        *   変更前: `scaleYVal := uint16(data[15])<<8 | uint16(data[16])`
        *   変更後: `scaleYVal := math.Float32frombits(binary.BigEndian.Uint32(data[20:24]))`
    *   offset 24: `blendMode`
        *   変更前: `blendMode := BlendMode(data[17])`
        *   変更後: `blendMode := BlendMode(data[24])`
    *   offset 25〜: `pixelData`
        *   変更前: `pixelData := data[18:]`
        *   変更後: `pixelData := data[25:]`
    *   `TransformBlit` 呼び出しの引数は型が合致するためそのまま

## Step-by-Step Implementation Guide

### Phase 1: テスト先行 — transform_test.go の更新

1.  **既存テストの引数を新型に変更**:
    *   Edit `features/neurom/internal/modules/vram/transform_test.go`
    *   `import` に `"math"` を追加
    *   `TestRotation90`: `TransformBlit(src, 4, 4, 2, 2, 64, 0x0100, 0x0100)` → `TransformBlit(src, 4, 4, 2, 2, float32(math.Pi/2), 1.0, 1.0)`
    *   `TestRotationWithCustomPivot`: 2箇所の `TransformBlit` 呼び出しを同様に変更
    *   `TestScale2x`: `TransformBlit(src, 2, 2, 1, 1, 0, 0x0200, 0x0200)` → `TransformBlit(src, 2, 2, 1, 1, 0, 2.0, 2.0)`
    *   `TestRotateAndScale`: `TransformBlit(src, 4, 4, 2, 2, 64, 0x0200, 0x0200)` → `TransformBlit(src, 4, 4, 2, 2, float32(math.Pi/2), 2.0, 2.0)`

2.  **新規テストの追加**:
    *   `TestArbitraryAngle`: 30°回転（`float32(math.Pi/6)`）で4×4ソースを変換。出力が非ゼロピクセルを含むことを確認。
    *   `TestArbitraryScale`: scaleX=1.5, scaleY=0.75 で4×4ソースを変換。出力幅が `4*1.5=6` 以上、出力高さが `4*0.75=3` 以上であることを確認。

3.  **ビルド失敗を確認** (TDD: Failed First):
    *   `./scripts/process/build.sh` を実行し、`TransformBlit` のシグネチャ不一致でコンパイルエラーになることを確認。

### Phase 2: transform.go の実装

4.  **関数シグネチャと内部ロジックの変更**:
    *   Edit `features/neurom/internal/modules/vram/transform.go`
    *   `rotation uint8` → `rotation float32`
    *   `scaleX, scaleY uint16` → `scaleX, scaleY float32`
    *   `angleRad := float64(rotation) / 256.0 * 2.0 * math.Pi` → `angleRad := float64(rotation)`
    *   `sx := float64(scaleX) / 256.0` → `sx := float64(scaleX)`
    *   `sy := float64(scaleY) / 256.0` → `sy := float64(scaleY)`
    *   `<= 0` ガードは維持

5.  **ビルド＆単体テスト実行**:
    *   `./scripts/process/build.sh` を実行。
    *   `transform_test.go` の全テストが PASS することを確認。
    *   `vram_test.go` 側はまだ旧型で呼んでいるためコンパイルエラーになる。これは Phase 3 で解消。

### Phase 3: テスト先行 — vram_test.go の更新

6.  **`buildBlitRectTransformMsg` ヘルパーの更新**:
    *   Edit `features/neurom/internal/modules/vram/vram_test.go`
    *   `import` に `"encoding/binary"` と `"math"` を追加
    *   シグネチャ: `rotation uint8, scaleX, scaleY uint16` → `rotation float32, scaleX, scaleY float32`
    *   データバッファ: `make([]byte, 18+len(pixels))` → `make([]byte, 25+len(pixels))`
    *   offset 12-15: `binary.BigEndian.PutUint32(data[12:16], math.Float32bits(rotation))`
    *   offset 16-19: `binary.BigEndian.PutUint32(data[16:20], math.Float32bits(scaleX))`
    *   offset 20-23: `binary.BigEndian.PutUint32(data[20:24], math.Float32bits(scaleY))`
    *   offset 24: `data[24] = blendMode`
    *   offset 25〜: `copy(data[25:], pixels)`

7.  **呼び出し箇所の引数を更新（7箇所）**:
    *   `buildBlitRectTransformMsg` 呼び出し（5箇所）:
        *   `TestBlitRectTransform`: `64, 0x0100, 0x0100` → `float32(math.Pi/2), float32(1.0), float32(1.0)`
        *   `TestTransformExact_NoRotation`: `0, 0x0100, 0x0100` → `0, float32(1.0), float32(1.0)`
        *   `TestTransformExact_Rotate90`（2箇所）: `64, 0x0100, 0x0100` → `float32(math.Pi/2), float32(1.0), float32(1.0)`
        *   `TestTransformExact_Scale2x`: `0, 0x0200, 0x0200` → `0, float32(2.0), float32(2.0)`
    *   `TransformBlit` 直接呼び出し（2箇所）:
        *   `TestTransformExact_Rotate90`: `TransformBlit(src, 4, 4, 2, 2, 64, 0x0100, 0x0100)` → `TransformBlit(src, 4, 4, 2, 2, float32(math.Pi/2), 1.0, 1.0)`
        *   `TestTransformExact_Scale2x`: `TransformBlit(src, 2, 2, 1, 1, 0, 0x0200, 0x0200)` → `TransformBlit(src, 2, 2, 1, 1, 0, 2.0, 2.0)`

8.  **ビルド失敗を確認** (TDD: Failed First):
    *   `./scripts/process/build.sh` を実行。テストコードはコンパイルされるが、`vram.go` の `handleBlitRectTransform` が旧フォーマットでパースするためテストが FAIL することを確認。

### Phase 4: vram.go の実装

9.  **`handleBlitRectTransform` のデシリアライズ更新**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`
    *   `import` に `"encoding/binary"` と `"math"` を追加
    *   コメントのフォーマット記述を更新:
        ```
        // Format: [dst_x:u16][dst_y:u16][src_w:u16][src_h:u16][pivot_x:u16][pivot_y:u16]
        //         [rotation:f32][scale_x:f32][scale_y:f32][blend_mode:u8][pixel_data...]
        ```
    *   最小長チェック: `len(data) < 18` → `len(data) < 25`
    *   rotation: `rotation := data[12]` → `rotation := math.Float32frombits(binary.BigEndian.Uint32(data[12:16]))`
    *   scaleX: `scaleXVal := uint16(data[13])<<8 | uint16(data[14])` → `scaleXVal := math.Float32frombits(binary.BigEndian.Uint32(data[16:20]))`
    *   scaleY: `scaleYVal := uint16(data[15])<<8 | uint16(data[16])` → `scaleYVal := math.Float32frombits(binary.BigEndian.Uint32(data[20:24]))`
    *   blendMode: `BlendMode(data[17])` → `BlendMode(data[24])`
    *   pixelData: `data[18:]` → `data[25:]`

### Phase 5: 全体検証

10. **ビルド＆全テスト実行**:
    *   `./scripts/process/build.sh` を実行し、全テストが PASS することを確認。

11. **統合テスト実行**:
    *   `./scripts/process/integration_test.sh` を実行し、リグレッションがないことを確認。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    Run the build script.
    ```bash
    ./scripts/process/build.sh
    ```
    *   **確認事項**:
        *   `transform_test.go` の全テスト（`TestRotation90`, `TestRotationWithCustomPivot`, `TestScale2x`, `TestRotateAndScale`, `TestArbitraryAngle`, `TestArbitraryScale`）が PASS
        *   `vram_test.go` のTransform関連テスト（`TestBlitRectTransform`, `TestTransformExact_NoRotation`, `TestTransformExact_Rotate90`, `TestTransformExact_Scale2x`）が PASS
        *   その他の既存テストにリグレッションがないこと

2.  **Integration Tests**:
    Run integration tests.
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   **確認事項**: 統合テストに `blit_rect_transform` を使用するものは現時点で存在しないが、他テストへのリグレッションがないことを確認。

## Documentation

#### [MODIFY] [006-VRAMEnhancement.md](file://prompts/phases/000-foundation/ideas/main/006-VRAMEnhancement.md)
*   **更新内容**: `blit_rect_transform` のワイヤフォーマット記述がある場合、新フォーマット（`rotation:f32`, `scale_x:f32`, `scale_y:f32`、ヘッダ25バイト）に更新する。

#### [MODIFY] [006-VRAMEnhancement-Part1.md](file://prompts/phases/000-foundation/plans/main/006-VRAMEnhancement-Part1.md)
*   **更新内容**: 同上。ワイヤフォーマットの記述がある場合に更新。

#### [MODIFY] [006-VRAMEnhancement-Part2.md](file://prompts/phases/000-foundation/plans/main/006-VRAMEnhancement-Part2.md)
*   **更新内容**: 同上。

#### [MODIFY] [008-VRAMOperationVerification.md](file://prompts/phases/000-foundation/ideas/main/008-VRAMOperationVerification.md)
*   **更新内容**: 同上。

#### [MODIFY] [008-VRAMOperationVerification.md](file://prompts/phases/000-foundation/plans/main/008-VRAMOperationVerification.md)
*   **更新内容**: 同上。
