# 008-VRAMOperationVerification

> **Source Specification**: prompts/phases/000-foundation/ideas/main/008-VRAMOperationVerification.md

## Goal Description

VRAMモジュールの4つの操作（BlitRect, Transform, AlphaBlend, Scroll/CopyRect）に対し、小さな領域（4x4〜8x4）で操作後のバッファ内容をピクセル単位で厳密検証する単体テストを追加する。既存テストが検出できなかったピクセル配置の誤り（転置・反転・オフセットミス等）を検出し、発見されたバグを修正する。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 1. 小さな領域での厳密検証（期待データ配列と1ピクセルずつ比較） | 全7テスト関数で `buffer` / `colorBuffer` を直接比較 |
| 2. 4つの操作カテゴリ（BlitRect, Transform, AlphaBlend, Scroll） | `TestBlitRectExact`, `TestTransformExact_*`, `TestAlphaBlendExact`, `TestScrollExact*` |
| 3. 異なるピクセル値の使用 | 各テストで `{1,2,...,16}` など全ピクセルが区別可能なパターンを使用 |
| 4. VRAMバッファの直接検証 | 同一パッケージ内テストのため `v.buffer`, `v.colorBuffer` に直接アクセス |
| 5. 周辺領域の非破壊確認 | `TestBlitRectExact` で隣接ピクセルが0であることを確認 |
| 7. エッジケース（VRAM境界クリッピング） | 必須要件の基本テストを優先し、本計画では対象外。基本テストで不具合が見つかれば別途計画する |

## Proposed Changes

### vram (unit tests)

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)
*   **Description**: 7つの厳密検証テスト関数を追加する
*   **Technical Design**: 既存のヘルパー `newTestVRAM()`, `buildBlitRectMsg()`, `buildBlitRectTransformMsg()` を再利用する。各テストは以下の共通パターンに従う:
    1. `newTestVRAM()` で新規 VRAMModule を作成
    2. 必要に応じてパレットを設定
    3. 操作を実行（`handleMessage` 経由）
    4. `v.buffer` / `v.colorBuffer` を期待配列と比較

---

#### テスト1: `TestBlitRectExact`

*   **目的**: BlitRect (Replace) でピクセルが正しい位置・正しい順序で書き込まれることを検証
*   **入力**:
    ```
    src (4x4):  {1,2,3,4, 5,6,7,8, 9,10,11,12, 13,14,15,1}
    dst座標: (10, 10), blend_mode=Replace(0x00)
    ```
*   **検証項目**:
    1. `v.buffer[(10+row)*256 + (10+col)]` が `src[row*4+col]` と一致（row,col ∈ [0,3]）
    2. `v.colorBuffer` の各ピクセル4バイトが `v.palette[src[row*4+col]]` の RGBA と一致
    3. 隣接4点 `(9,10)`, `(14,10)`, `(10,9)`, `(10,14)` の buffer 値が全て 0

---

#### テスト2: `TestTransformExact_NoRotation`

*   **目的**: Transform (回転0°、スケール1x) が BlitRect と同一のピクセル配置を生むことを確認（基準テスト）
*   **入力**:
    ```
    src (4x4): {1,2,3,4, 5,6,7,8, 9,10,11,12, 13,14,15,16}
    pivot=(0,0), rotation=0, scaleX=0x0100(1.0), scaleY=0x0100(1.0)
    blend_mode=Replace(0x00), dst座標=(10,10)
    ```
*   **期待動作**:
    - `TransformBlit` の出力が入力と同一（4x4、offset=(0,0)）
    - VRAMの `(10,10)` ～ `(13,13)` が src パターンと一致
*   **検証項目**:
    1. `v.buffer[(10+row)*256 + (10+col)]` が `src[row*4+col]` と一致
    2. 0 のソースピクセルは skip されるため、パレットインデックス 0 は避ける（全て 1〜16 を使用）

---

#### テスト3: `TestTransformExact_Rotate90`

*   **目的**: 90度回転後のピクセル配置が数学的に正しいことを厳密検証
*   **入力**:
    ```
    src (4x4): {1,2,3,4, 5,6,7,8, 9,10,11,12, 13,14,15,16}
    pivot=(2,2), rotation=64(=90°), scaleX=0x0100, scaleY=0x0100
    blend_mode=Replace(0x00)
    ```
*   **検証方法**:
    1. まず `TransformBlit` を直接呼び出し、出力バッファ・サイズ・オフセットを取得
    2. 90° CW 回転の数学的期待値を計算: `dst(x,y) = src(y, srcH-1-x)` の関係をベースに、アルゴリズムのピクセルセンター補正を考慮
    3. 出力配列の各ピクセルを期待値と比較
    4. 非ゼロピクセルの総数が 16（全ソースピクセルが保存される）であることを確認。もし 16 未満なら TransformBlit にバグがある

*   **期待出力の導出** (TransformBlit のアルゴリズムに基づく):
    - 逆変換: `srcFX = fy + pivotX`, `srcFY = -fx + pivotY` (cos90°≈0, sin90°=1)
    - `fx = outX + minX`, `fy = outY + minY`
    - minX, minY, dstW, dstH は前方変換のバウンディングボックスから算出
    - 各 (outX, outY) に対し `floor(srcFX)`, `floor(srcFY)` で nearest-neighbor サンプリング
    - ソース座標が [0, srcW) × [0, srcH) の範囲外なら 0

*   **バグ検出の意図**: 現在のアルゴリズムではバウンディングボックスが「ピクセル境界」ベースで計算されるため、ソースの一部ピクセルが出力に含まれない可能性がある。テストで 16 ピクセル未満であればバグとして報告・修正する

---

#### テスト4: `TestTransformExact_Scale2x`

*   **目的**: 2倍スケールが nearest-neighbor 拡大として正しく動作することを検証
*   **入力**:
    ```
    src (2x2): {1,2, 3,4}
    pivot=(1,1), rotation=0, scaleX=0x0200(2.0), scaleY=0x0200(2.0)
    blend_mode=Replace(0x00)
    ```
*   **期待出力** (TransformBlit 直接呼び出し):
    ```
    4x4:
    1 1 2 2
    1 1 2 2
    3 3 4 4
    3 3 4 4
    ```
    offset=(-2, -2)
*   **検証項目**:
    1. `TransformBlit` の出力配列が上記 4x4 パターンと一致
    2. `handleBlitRectTransform` 経由で VRAM に書き込んだ場合、`(dstX+offsetX, dstY+offsetY)` から 4x4 の範囲が期待パターンと一致

---

#### テスト5: `TestAlphaBlendExact`

*   **目的**: AlphaBlend 後の colorBuffer RGBA 値が演算式通りであることを厳密検証
*   **入力**:
    ```
    palette[1] = (255, 0, 0, 255)   // 赤 (不透明)
    palette[2] = (0, 0, 255, 128)   // 青 (半透明)
    ```
    1. `blit_rect(Replace)` で index=1 の 2x2 を (20,20) に書き込み
    2. `blit_rect(AlphaBlend)` で index=2 の 2x2 を (20,20) に上書き
*   **期待 colorBuffer 値** (AlphaBlend 式: `dst = src*α/255 + dst*(255-α)/255`):
    ```
    R = (0 * 128 + 255 * 127) / 255 = 127
    G = (0 * 128 + 0 * 127) / 255   = 0
    B = (255 * 128 + 0 * 127) / 255 = 128
    A = 255
    ```
*   **検証項目**:
    1. 4ピクセル全ての `v.buffer[idx]` が `0xFF`（ダイレクトカラーマーカー）
    2. 4ピクセル全ての `v.colorBuffer[idx*4 .. idx*4+3]` が `(127, 0, 128, 255)` と一致（許容誤差 ±1）
    3. 隣接ピクセル (19,20), (22,20) の buffer が 0 のまま

---

#### テスト6: `TestScrollExact`

*   **目的**: CopyRect によるスクロール操作後のバッファ内容を厳密検証
*   **セットアップ**:
    ```
    8x4 領域の各行に blit_rect(Replace) で異なる値を設定:
      row 0 (y=10): 全ピクセル = 10
      row 1 (y=11): 全ピクセル = 20
      row 2 (y=12): 全ピクセル = 30
      row 3 (y=13): 全ピクセル = 40
    x 範囲: [5, 12]
    ```
*   **操作**: `copy_rect` src=(5,10) → dst=(5,11), size=8x3 （1行下にスクロール）
*   **期待結果**:
    ```
    row y=10: 10 (変更なし — コピー元だが上書きされない)
    row y=11: 10 (元の row 0 からコピー)
    row y=12: 20 (元の row 1 からコピー)
    row y=13: 30 (元の row 2 からコピー)
    ```
*   **検証項目**:
    1. `v.buffer[(10)*256 + (5+col)]` = 10 (col ∈ [0,7])
    2. `v.buffer[(11)*256 + (5+col)]` = 10
    3. `v.buffer[(12)*256 + (5+col)]` = 20
    4. `v.buffer[(13)*256 + (5+col)]` = 30
    5. `v.colorBuffer` も対応する `palette[value]` と一致

---

#### テスト7: `TestScrollExact_Overlap`

*   **目的**: オーバーラップするコピー（上方向スクロール）で一時バッファが正しく機能することを検証
*   **セットアップ**: テスト6と同じ 8x4 領域
*   **操作**: `copy_rect` src=(5,11) → dst=(5,10), size=8x3 （1行上にスクロール）
*   **期待結果**:
    ```
    row y=10: 20 (元の row 1 からコピー)
    row y=11: 30 (元の row 2 からコピー)
    row y=12: 40 (元の row 3 からコピー)
    row y=13: 40 (変更なし)
    ```
*   **検証項目**: 一時バッファによるコピーのため、ソースが宛先に上書きされても正しい値が保持されること

---

### vram (potential bug fixes)

#### [MODIFY] [features/neurom/internal/modules/vram/transform.go](file://features/neurom/internal/modules/vram/transform.go)
*   **Description**: テスト3 (`TestTransformExact_Rotate90`) で全16ピクセルが出力に含まれない場合、バウンディングボックス計算またはサンプリング座標のオフセットを修正する
*   **潜在的修正箇所**:
    - 前方変換のコーナー座標: 現在 `(0,0)` 〜 `(srcW, srcH)` をピクセル境界として使用しているが、ピクセルセンター `(0.5, 0.5)` 〜 `(srcW-0.5, srcH-0.5)` に変更、または出力座標のサンプリング時に `+0.5` のオフセットを追加する可能性
    - 逆変換のサンプリング: `floor()` ではなく `round()` / `floor(x+0.5)` が適切な場合がある
*   **修正方針**: テスト結果を見て具体的な修正を決定する（TDD）

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)
*   **Description**: テスト1〜7 の実行で BlitRect, CopyRect, AlphaBlend のいずれかにバグが見つかった場合に修正
*   **修正方針**: テスト結果を見て具体的な修正を決定する（TDD）

## Step-by-Step Implementation Guide

### Phase 1: テスト作成（TDD — Red）

- [x] **Step 1: BlitRect 厳密テスト**
    * `vram_test.go` に `TestBlitRectExact` を追加
    * ソースパターン `{1,2,3,4, 5,6,7,8, 9,10,11,12, 13,14,15,1}` を使用
    * `buildBlitRectMsg(10, 10, 4, 4, 0x00, src)` でメッセージ構築
    * `handleMessage` で実行後、`v.buffer` と `v.colorBuffer` を期待値と比較
    * 隣接ピクセル4点のゼロ確認

- [x] **Step 2: Transform 基準テスト（回転なし）**
    * `TestTransformExact_NoRotation` を追加
    * `buildBlitRectTransformMsg(10, 10, 4, 4, 0, 0, 0, 0x0100, 0x0100, 0x00, src)` を使用
    * 回転0°・スケール1xのため、BlitRect と同じ結果を期待
    * `v.buffer` の `(10,10)`〜`(13,13)` を比較

- [x] **Step 3: Transform 90度回転テスト**
    * `TestTransformExact_Rotate90` を追加
    * まず `TransformBlit` を直接呼び出して出力を取得
    * 出力の非ゼロピクセル数が 16 であることを確認
    * 各ピクセルの期待値を逆変換式から計算し比較
    * `handleBlitRectTransform` 経由でのVRAM書き込みも検証

- [x] **Step 4: Transform 2倍スケールテスト**
    * `TestTransformExact_Scale2x` を追加
    * `TransformBlit` 直接呼び出しで 4x4 の nearest-neighbor 拡大結果 `{1,1,2,2, 1,1,2,2, 3,3,4,4, 3,3,4,4}` と比較
    * VRAM 書き込み結果も検証

- [x] **Step 5: AlphaBlend 厳密テスト**
    * `TestAlphaBlendExact` を追加
    * パレット設定 → 赤 Replace 書き込み → 青 AlphaBlend 上書き
    * `v.buffer` のダイレクトカラーマーカー (0xFF) 確認
    * `v.colorBuffer` の RGBA を `(127, 0, 128, 255)` と比較（許容 ±1）

- [x] **Step 6: Scroll 厳密テスト**
    * `TestScrollExact` を追加
    * 8x4 領域に行ごとの値を blit_rect で書き込み
    * `copy_rect` で1行下スクロール
    * 全ピクセルの buffer 値を期待配列と比較

- [x] **Step 7: Scroll オーバーラップテスト**
    * `TestScrollExact_Overlap` を追加
    * 同じ領域で1行上スクロール
    * 一時バッファが正しく機能していることを全ピクセル比較で確認

### Phase 2: ビルドと初回テスト実行

- [x] **Step 8: ビルドとテスト実行**
    * `./scripts/process/build.sh` を実行
    * 失敗したテストを記録

### Phase 3: バグ修正（TDD — Green）

- [x] **Step 9: 失敗テストの分析と修正**
    * 各失敗テストの出力（expected vs actual）から根本原因を特定
    * `transform.go` / `vram.go` を修正
    * `./scripts/process/build.sh` で修正確認

### Phase 4: リグレッション確認

- [x] **Step 10: 全テスト再実行**
    * `./scripts/process/build.sh` で全テスト通過を確認
    * 既存テスト（`TestBlitRect`, `TestRotation90`, `TestBlitRectWithBlend`, `TestCopyRect` 等）もリグレッションなしを確認

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```
    *   **確認項目**: 新規追加の7テスト (`TestBlitRectExact`, `TestTransformExact_NoRotation`, `TestTransformExact_Rotate90`, `TestTransformExact_Scale2x`, `TestAlphaBlendExact`, `TestScrollExact`, `TestScrollExact_Overlap`) が全て PASS
    *   **確認項目**: 既存テスト (`TestBlitRect`, `TestRotation90`, `TestScale2x`, `TestBlitRectWithBlend`, `TestCopyRect` 等) にリグレッションなし

2.  **Integration Tests** (任意):
    ```bash
    ./scripts/process/integration_test.sh --specify "TestBlitAndReadRect|TestAlphaBlendPipeline"
    ```
    *   **確認項目**: 既存統合テストが引き続き PASS

## Documentation

本計画の範囲ではドキュメント更新は不要。テストのみの追加とバグ修正のため。
