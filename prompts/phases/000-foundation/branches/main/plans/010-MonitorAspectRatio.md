# 010-MonitorAspectRatio

> **Source Specification**: [prompts/phases/000-foundation/ideas/main/010-MonitorAspectRatio.md](file://prompts/phases/000-foundation/ideas/main/010-MonitorAspectRatio.md)

## Goal Description

モニターモジュールのウインドウリサイズ時に、VRAMのアスペクト比（256:212）を維持した描画を実現する。余白部分は黒帯（レターボックス/ピラーボックス）で表示し、コンテンツはウインドウ中央に配置する。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R1: アスペクト比維持描画 (256:212) | Proposed Changes > `monitor.go` — `calcViewport` 関数 |
| R2: レターボックス/ピラーボックス表示 | Proposed Changes > `monitor.go` — `calcViewport` 関数 + `onPaint` のビューポート設定 |
| R3: コンテンツの中央配置 | Proposed Changes > `monitor.go` — `calcViewport` のオフセット計算 |
| R4: GL_NEAREST フィルタリング維持 | 変更なし（既存の `onStart` の設定を保持、本計画では触れない） |
| R5: 整数倍スケーリング（将来対応） | 本計画では実装しない。`calcViewport` を純粋関数として分離することで将来の拡張ポイントを提供 |

## Proposed Changes

### monitor module

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor_test.go](file://features/neurom/internal/modules/monitor/monitor_test.go)

*   **Description**: `calcViewport` 関数のテーブル駆動単体テストを追加する。
*   **Technical Design**:
    *   ```go
        func TestCalcViewport(t *testing.T)
        // テーブル駆動テスト
        ```
*   **Logic**:
    *   テストケース:

    | Name | vramW | vramH | winW | winH | 期待: offsetX | offsetY | viewW | viewH |
    | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
    | wide window (pillarbox) | 256 | 212 | 800 | 400 | 158 | 0 | 483 | 400 |
    | tall window (letterbox) | 256 | 212 | 400 | 800 | 0 | 234 | 400 | 331 |
    | square window | 256 | 212 | 500 | 500 | 0 | 43 | 500 | 414 |
    | exact 2x aspect ratio | 256 | 212 | 512 | 424 | 0 | 0 | 512 | 424 |
    | exact 1x (native) | 256 | 212 | 256 | 212 | 0 | 0 | 256 | 212 |
    | minimum 1x1 | 256 | 212 | 1 | 1 | 0 | 0 | 1 | 0 |
    | zero width | 256 | 212 | 0 | 500 | 0 | 250 | 0 | 0 |
    | zero height | 256 | 212 | 500 | 0 | 250 | 0 | 0 | 0 |

    *   各テストケースで `calcViewport(tt.vramW, tt.vramH, tt.winW, tt.winH)` を呼び出し、返却値 `(offsetX, offsetY, viewW, viewH)` が期待値と一致することを検証する。
    *   ゼロ除算が発生しないことを確認する（winW=0, winH=0 のケース）。

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor.go](file://features/neurom/internal/modules/monitor/monitor.go)

*   **Description**: ビューポート計算関数の追加、構造体へのウインドウサイズフィールドの追加、`size.Event` ハンドラと `onPaint` の修正。
*   **Technical Design**:
    *   **新規関数 `calcViewport`**:
        ```go
        // calcViewport computes the GL viewport rectangle that preserves the VRAM aspect ratio
        // within the given window dimensions. Returns (offsetX, offsetY, viewW, viewH).
        func calcViewport(vramW, vramH, winW, winH int) (int, int, int, int)
        ```
    *   **構造体フィールド追加** (`MonitorModule`):
        ```go
        winWidth  int  // current window width in pixels
        winHeight int  // current window height in pixels
        ```
*   **Logic**:

    **`calcViewport` のアルゴリズム** (整数演算による浮動小数点回避):

    1. `winW <= 0` または `winH <= 0` の場合: `(winW/2, winH/2, 0, 0)` を返却（ゼロ除算防止）
    2. クロス乗算で比較: `winW * vramH` vs `winH * vramW`
       - `winW * vramH > winH * vramW` の場合（ウインドウが横長 → ピラーボックス）:
         - `viewH = winH`
         - `viewW = winH * vramW / vramH`
         - `offsetX = (winW - viewW) / 2`
         - `offsetY = 0`
       - それ以外（ウインドウが縦長または一致 → レターボックスまたはフル表示）:
         - `viewW = winW`
         - `viewH = winW * vramH / vramW`
         - `offsetX = 0`
         - `offsetY = (winH - viewH) / 2`

    **検算**:
    - 横長 800×400: `800*212=169600 > 400*256=102400` → pillarbox。`viewW = 400*256/212 = 483`, `offsetX = (800-483)/2 = 158` ✓
    - 縦長 400×800: `400*212=84800 < 800*256=204800` → letterbox。`viewH = 400*212/256 = 331`, `offsetY = (800-331)/2 = 234` ✓
    - 正方形 500×500: `500*212=106000 < 500*256=128000` → letterbox。`viewH = 500*212/256 = 414`, `offsetY = (500-414)/2 = 43` ✓
    - 完全一致 512×424: `512*212=108544 == 424*256=108544` → else 分岐（フル表示）。`viewH = 512*212/256 = 424`, `offsetY = 0` ✓

    **`size.Event` ハンドラの変更**:
    - `m.winWidth`, `m.winHeight` にウインドウサイズを保存する。
    - ビューポートの設定自体は `onPaint` 内で行う（ハンドラ内では `Viewport` を呼ばない）。

    **`onPaint` の変更**:
    - 既存の `glctx.ClearColor(0,0,0,1)` + `glctx.Clear(gl.COLOR_BUFFER_BIT)` はそのまま維持（`glClear` はビューポートに関係なくフレームバッファ全体をクリアするため、黒帯部分も自動的にクリアされる）。
    - `glctx.Clear()` の後、描画コードの前に `calcViewport` でビューポートを計算し `glctx.Viewport()` を設定する。

## Step-by-Step Implementation Guide

- [x] 1. **単体テスト作成 (Red)**:
    * Edit `features/neurom/internal/modules/monitor/monitor_test.go` に `TestCalcViewport` を追加。
    * テーブル駆動テスト (8ケース: wide, tall, square, exact 2x, native 1x, minimum, zero width, zero height)。
    * この時点では `calcViewport` が未実装なのでコンパイルエラーとなる。

- [x] 2. **`calcViewport` 関数の実装 (Green)**:
    * Edit `features/neurom/internal/modules/monitor/monitor.go` に `calcViewport` 関数を追加。
    * 整数演算のクロス乗算アルゴリズムを実装（Proposed Changes のロジック参照）。

- [x] 3. **ビルド＆テスト実行 (Green 確認)**:
    * `./scripts/process/build.sh` を実行。
    * `TestCalcViewport` が全ケース PASS することを確認。

- [x] 4. **構造体フィールド追加**:
    * Edit `features/neurom/internal/modules/monitor/monitor.go` の `MonitorModule` 構造体に `winWidth`, `winHeight int` フィールドを追加。

- [x] 5. **`size.Event` ハンドラ修正**:
    * Edit `features/neurom/internal/modules/monitor/monitor.go` の `RunMain` 内 `case size.Event:` を修正。
    * `m.winWidth = e.WidthPx`, `m.winHeight = e.HeightPx` を保存する。
    * 既存の `m.glctx.Viewport(0, 0, e.WidthPx, e.HeightPx)` を削除する。

- [x] 6. **`onPaint` 修正**:
    * Edit `features/neurom/internal/modules/monitor/monitor.go` の `onPaint` メソッドを修正。
    * `glctx.Clear()` の後、描画コードの前に以下を追加:
      ```go
      ox, oy, vw, vh := calcViewport(m.width, m.height, m.winWidth, m.winHeight)
      glctx.Viewport(ox, oy, vw, vh)
      ```

- [x] 7. **最終ビルド＆テスト**:
    * `./scripts/process/build.sh` を実行。
    * 全テスト PASS、ビルド成功を確認。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```
    *   `TestCalcViewport` が全 8 ケース PASS すること。
    *   既存の `TestMonitorHeadless` が引き続き PASS すること。

2.  **Integration Tests**:
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   本変更は純粋な描画ロジックであり、統合テストへの影響はないが、リグレッション確認のため実行する。

### Manual Verification (例外事項)

OpenGL のビューポート描画結果はヘッドレス環境では検証不可能であるため、以下は手動確認とする:
*   ウインドウを横長にリサイズし、左右に黒帯が表示されること。
*   ウインドウを縦長にリサイズし、上下に黒帯が表示されること。
*   ウインドウを正方形にリサイズし、上下に黒帯が表示されること。
*   ウインドウを 512×424 にリサイズし、黒帯なしで全面描画されること。

## Documentation

本計画で影響を受ける既存ドキュメントはない。
