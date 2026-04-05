# 011: Transform パラメータの浮動小数点精度への変更

## 背景

現在の `TransformBlit` 関数では、回転角度を `uint8`（0〜255）で受け取り、内部で `0〜2π` にマッピングしている。

```go
// transform.go (現在)
rotation uint8,
// ...
angleRad := float64(rotation) / 256.0 * 2.0 * math.Pi
```

この方式では角度の分解能が **256段階（約1.41°刻み）** しかなく、滑らかな回転アニメーションや精密な角度指定が困難である。例えば、1°単位の指定すら正確にはできず、45°（= 32）や90°（= 64）などの特定角度しか正確に表現できない。

同様に、拡大率（`scaleX`, `scaleY`）も `uint16` の8.8固定小数点形式で表現されている。

```go
// transform.go (現在)
scaleX, scaleY uint16,
// ...
sx := float64(scaleX) / 256.0
sy := float64(scaleY) / 256.0
```

この形式では精度は 1/256 ≈ 0.0039 刻みであり、回転角度ほど深刻ではないが、実在のハードウェアをエミュレーションしているわけではないため、パラメータをバイトコード的な固定小数点で扱う必然性がない。

バスメッセージの `blit_rect_transform` コマンドにおいても、各パラメータはバイト列として送信されている。

```
// vram.go (現在のワイヤフォーマット)
// Format: [dst_x:u16][dst_y:u16][src_w:u16][src_h:u16][pivot_x:u16][pivot_y:u16]
//         [rotation:u8][scale_x:u16][scale_y:u16][blend_mode:u8][pixel_data...]
```

## 要件

### 必須要件

1. **回転角度の型を `float32` に変更**: `TransformBlit` 関数の `rotation` 引数を `uint8` から `float32` に変更する。値はラジアンで直接指定する。
2. **拡大率の型を `float32` に変更**: `TransformBlit` 関数の `scaleX`, `scaleY` 引数を `uint16`（8.8固定小数点）から `float32` に変更する。`1.0` が等倍を意味する。
3. **ワイヤフォーマットの更新**: `blit_rect_transform` メッセージの回転フィールドを `u8`（1バイト）→ `f32`（4バイト）、拡大率フィールドを `u16`（2バイト×2）→ `f32`（4バイト×2）に変更する。すべて IEEE 754 ビッグエンディアン。
4. **既存テストの更新**: 回転・拡大率に関連するすべてのテストを新しい型に合わせて修正する。
5. **後方互換性は不要**: 現在開発初期フェーズであり、旧フォーマットとの互換性維持は不要。

### 任意要件

なし。

## 実現方針

### 変更対象ファイルと概要

| ファイル | 変更内容 |
|---|---|
| `transform.go` | `rotation` を `uint8` → `float32`（ラジアン）、`scaleX/scaleY` を `uint16` → `float32`（倍率直値）に変更。内部の変換ロジックを削除。 |
| `vram.go` | `handleBlitRectTransform` のパースロジックを変更。回転・拡大率をそれぞれ4バイト（IEEE 754 float32 ビッグエンディアン）として読み取り。ヘッダ最小長を 18 → 25 に変更。 |
| `transform_test.go` | テストケースの `rotation` をラジアン、`scaleX/scaleY` を `float32` 倍率に変更。 |
| `vram_test.go` | `buildBlitRectTransformMsg` ヘルパーと関連テストを新フォーマットに対応。 |

### TransformBlit 関数シグネチャの変更

```go
// 変更前
func TransformBlit(
    src []uint8, srcW, srcH int,
    pivotX, pivotY int,
    rotation uint8,
    scaleX, scaleY uint16,
) (dst []uint8, dstW, dstH, offsetX, offsetY int)

// 変更後
func TransformBlit(
    src []uint8, srcW, srcH int,
    pivotX, pivotY int,
    rotation float32,
    scaleX, scaleY float32,
) (dst []uint8, dstW, dstH, offsetX, offsetY int)
```

内部計算は現在も `float64` で行っているため、`float32` → `float64` への変換のみで十分。

```go
// 回転: 変更前
angleRad := float64(rotation) / 256.0 * 2.0 * math.Pi
// 回転: 変更後
angleRad := float64(rotation)

// 拡大率: 変更前
sx := float64(scaleX) / 256.0
sy := float64(scaleY) / 256.0
// 拡大率: 変更後
sx := float64(scaleX)
sy := float64(scaleY)
```

`scaleX/scaleY` の `<= 0` ガード（デフォルト値 `1.0` へのフォールバック）は引き続き維持する。

### ワイヤフォーマットの変更

```
変更前（ヘッダ18バイト）:
[dst_x:u16][dst_y:u16][src_w:u16][src_h:u16][pivot_x:u16][pivot_y:u16]
[rotation:u8][scale_x:u16][scale_y:u16][blend_mode:u8][pixel_data...]
 offset: 12   13         15         17

変更後（ヘッダ25バイト）:
[dst_x:u16][dst_y:u16][src_w:u16][src_h:u16][pivot_x:u16][pivot_y:u16]
[rotation:f32][scale_x:f32][scale_y:f32][blend_mode:u8][pixel_data...]
 offset: 12      16         20         24
```

`float32` のデシリアライズには `math.Float32frombits` と `binary.BigEndian.Uint32` を使用する。

```go
import (
    "encoding/binary"
    "math"
)

rotBits := binary.BigEndian.Uint32(data[12:16])
rotation := math.Float32frombits(rotBits)

sxBits := binary.BigEndian.Uint32(data[16:20])
scaleX := math.Float32frombits(sxBits)

syBits := binary.BigEndian.Uint32(data[20:24])
scaleY := math.Float32frombits(syBits)

blendMode := BlendMode(data[24])
pixelData := data[25:]
```

### テスト値の変換対応表

#### 回転角度

| 旧値 (`uint8`) | 角度 | 新値 (`float32` ラジアン) | Go 定数表現 |
|---|---|---|---|
| `0` | 0° | `0` | `0` |
| `64` | 90° | `π/2 ≈ 1.5707964` | `math.Pi / 2` |
| `128` | 180° | `π ≈ 3.1415927` | `math.Pi` |
| `192` | 270° | `3π/2 ≈ 4.712389` | `3 * math.Pi / 2` |

#### 拡大率

| 旧値 (`uint16` 8.8固定小数点) | 倍率 | 新値 (`float32`) |
|---|---|---|
| `0x0100` (256) | 1.0倍（等倍） | `1.0` |
| `0x0200` (512) | 2.0倍 | `2.0` |
| `0x0080` (128) | 0.5倍 | `0.5` |

## 検証シナリオ

1. **90°回転の正確性**
   1. 4×4のソース画像をピボット(2,2)で90°（`π/2` ラジアン）回転する
   2. 変換後のピクセルが全て非ゼロであることを確認する
   3. 元の全16ピクセル値が変換後にも存在することを確認する

2. **回転なし（0ラジアン）の動作**
   1. 回転角度 `0` でスケール等倍の変換を実行する
   2. 出力が入力と同一であることを確認する

3. **カスタムピボットでの回転**
   1. 異なるピボット位置（中心 vs 左上）で同一角度の回転を実行する
   2. オフセット値が異なることを確認する（ピボット位置が反映されている）

4. **回転+スケールの組み合わせ**
   1. 2倍スケール + 90°回転を実行する
   2. 出力サイズが元画像の約2倍であることを確認する

5. **ワイヤフォーマット経由の回転**
   1. `blit_rect_transform` メッセージを新フォーマットで構築する
   2. VRAMモジュールに送信し、ピクセルが正しく配置されることを確認する

6. **任意角度の精密回転**（新規テスト）
   1. 旧フォーマットでは表現不可能な角度（例: 1°= `π/180` ラジアン、30°= `π/6` ラジアン）で回転を実行する
   2. 出力バッファが生成され、非ゼロピクセルが存在することを確認する

7. **任意倍率のスケーリング**（新規テスト）
   1. 旧固定小数点形式では端数になる倍率（例: 1.5倍、0.75倍）でスケーリングを実行する
   2. 出力サイズが期待通りであることを確認する

8. **スケール等倍（`1.0`）の動作確認**
   1. 回転なし（`0`）・スケール `1.0` の変換を実行する
   2. 出力が入力と同一であることを確認する（旧 `0x0100` 相当の動作）

## テスト項目

### 単体テスト

| テストケース | 検証内容 | 対応要件 |
|---|---|---|
| `TestRotation90` | `float32` の `π/2` で90°回転が正しく動作する | 要件1 |
| `TestRotationWithCustomPivot` | ピボット位置の違いが反映される | 要件1 |
| `TestRotateAndScale` | `float32` の回転値+`float32` のスケール値の組み合わせ | 要件1, 2 |
| `TestTransformExact_NoRotation` | 回転なし（`0`）・スケール等倍（`1.0`）で入力が保持される | 要件1, 2 |
| `TestTransformExact_Rotate90` | 90°回転のピクセル配置が正確 | 要件1 |
| `TestBlitRectTransform` | 新ワイヤフォーマット（f32×3）でのメッセージ処理 | 要件3 |
| `TestTransformExact_NoRotation`（VRAM経由） | メッセージ経由での回転なし・等倍動作 | 要件3 |
| 新規: 任意角度テスト | 1°、30°など旧形式で不可能だった角度 | 要件1 |
| 新規: 任意スケールテスト | 1.5倍、0.75倍など旧固定小数点で端数になる倍率 | 要件2 |

テスト実行コマンド:

```bash
scripts/process/build.sh
```

### 統合テスト

既存の統合テストのうち、`blit_rect_transform` メッセージを使用するものがあれば、新フォーマットに更新する。

テスト実行コマンド:

```bash
scripts/process/integration_test.sh
```
