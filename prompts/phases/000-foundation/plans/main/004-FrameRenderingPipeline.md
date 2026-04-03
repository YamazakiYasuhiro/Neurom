# 004-FrameRenderingPipeline

> **Source Specification**: prompts/phases/000-foundation/ideas/main/004-FrameRenderingPipeline.md

## Goal Description
Monitor モジュールのアーキテクチャを改修し、バスイベント（VRAMやパレットの更新）のたびにRGB変換を行う「即時反映方式」から、画面への描画（VSYNC/Paintイベント発生時）の直前にVRAMとパレットを合成して1フレームのRGBバッファを作り上げる「遅延合成（フレームパイプライン）方式」に変更します。

## User Review Required
None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| VRAMと描画の分離 | `receiveLoop` からRGBAの上書きコード（および全走査ループ）を削除 |
| フレーム一括合成 | 新設する `buildFrame()` メソッドおよび `onPaint` での呼び出し |
| Headless モードへの配慮 | `GetPixel` の際に `m.dirty` であれば `buildFrame()` を自動で呼ぶ処理の追加 |

## Proposed Changes

### Monitor Module

#### [MODIFY] features/vm/internal/modules/monitor/monitor.go
*   **Description**: `receiveLoop` における即時マッピング処理を削除し、代わりに `buildFrame` による一括描画処理を実装します。あわせて `paint.Event` の発行回数を間引く最適化（dirty判定の工夫）を導入します。
*   **Technical Design**:
    *   ```go
        func (m *MonitorModule) buildFrame() {
            // 前提として呼び出し元で m.mu.Lock() が取得されていること
            for i := 0; i < len(m.vram); i++ {
                p := m.vram[i]
                m.rgba[i*4]   = m.palette[p][0]
                m.rgba[i*4+1] = m.palette[p][1]
                m.rgba[i*4+2] = m.palette[p][2]
                m.rgba[i*4+3] = 255
            }
        }
        ```
*   **Logic**:
    *   **receiveLoop の修正**:
        *   `palette_updated` 受信時: パレット配列を更新後、`rgba` バッファを全走査して上書きしていた `for` ループを削除する。
        *   `vram_updated` 受信時: `r, g, b` をルックアップして `rgba` バッファに代入していた処理を削除する。
        *   両方の更新ルートにおいて、`m.dirty = true` にする前にもともと false だったか (`wasDirty := m.dirty`) を判定し、もともと false の場合のみ `paint.Event{}` を Send するように最適化する（連続描画によるUIイベントキューのフラッドを防ぐため）。
    *   **onPaint の修正**:
        *   `if m.dirty { ... }` ブロックの先頭で、新たに実装する `m.buildFrame()` を呼び出す。
    *   **GetPixel の修正**:
        *   Headless（テスト）の際に合成結果が取得できるよう、`m.mu.Lock()` 後に `if m.dirty { m.buildFrame(); m.dirty = false }` の処理を追加する。

## Step-by-Step Implementation Guide

1.  **[x] Step 1: Implement buildFrame & Modify GetPixel**:
    *   Edit `features/vm/internal/modules/monitor/monitor.go`.
    *   ロック不要（呼び出し元依存）の `buildFrame()` メソッドを追加する。
    *   `GetPixel` の先頭（ロック直後）に `if m.dirty` 判定を追加し、必要に応じて `buildFrame()` を呼び出して `dirty=false` とするロジックを追加する。
2.  **[x] Step 2: Modify onPaint**:
    *   Edit `features/vm/internal/modules/monitor/monitor.go`.
    *   `onPaint` メソッドの `if m.dirty` ブロック内に `m.buildFrame()` 関数の呼び出しを追加し、テクスチャへの転送時に最新のRGBバッファを作り上げるようにする。
3.  **[x] Step 3: Simplify receiveLoop**:
    *   Edit `features/vm/internal/modules/monitor/monitor.go`.
    *   `receiveLoop` の `palette_updated` ブランチから `rgba` 上塗りの for ループを削除。
    *   `vram_updated` ブランチから `rgba` への即時反映を削除。
    *   両方のブランチで、不要な `paint.Event` 送信を防ぐため、`wasDirty := m.dirty` 変数を用いて、未描画状態から Dirty に遷移したときのみイベントを投げるように変更する。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    run the build script.
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests**:
    Run integration tests. `palette_test.go` は統合テスト内で `GetPixel` を呼び出すため、今回の Headless 対応パイプラインの正しさをそのまま証明するテストになります。
    ```bash
    ./scripts/process/integration_test.sh --specify "TestPaletteUpdate"
    ```
    *   **Log Verification**: テストが通過（PASS）し、赤色（255, 0, 0）が正常に取得されること。

## Documentation

*更新対象の仕様ドキュメントはありません。*
